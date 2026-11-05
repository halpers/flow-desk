package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/google/uuid"
)

// Opt in only against a disposable S3 sandbox, never the developer's real bucket.
func TestS3SandboxRetirementContracts(t *testing.T) {
	if os.Getenv("FLOWDESK_S3_SANDBOX_TESTS") != "1" {
		t.Skip("requires explicit disposable S3 sandbox")
	}
	if os.Getenv("MODEL_PROVIDER") != "mock" {
		t.Fatal("sandbox requires mock configuration")
	}
	store, err := NewS3SourceStore(S3SourceConfig{Endpoint: os.Getenv("FILE_STORE_ENDPOINT"), Region: os.Getenv("FILE_STORE_REGION"), Bucket: os.Getenv("FILE_STORE_BUCKET"), AccessKey: os.Getenv("FILE_STORE_ACCESS_KEY"), SecretKey: os.Getenv("FILE_STORE_SECRET_KEY"), AllowHTTP: os.Getenv("FILE_STORE_ALLOW_HTTP") == "true"})
	if err != nil {
		t.Fatal(err)
	}
	source := store.(*s3SourceStore)
	ctx := context.Background()
	if err = source.CheckRetirement(ctx); err != nil {
		t.Fatal(err)
	}
	raw := []byte("%PDF synthetic disposable compatibility probe")
	digest := sha256.Sum256(raw)
	hash := hex.EncodeToString(digest[:])
	id := uuid.NewString()
	tenant := uuid.NewString()
	prefix := "knowledge/" + tenant + "/" + id + "/"
	key := prefix + hash + "/0.pdf"
	if err = source.PutIfAbsent(ctx, key, raw); err != nil {
		t.Fatal(err)
	}
	_, err = source.api.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(source.bucket), Key: aws.String(key), Body: bytes.NewReader([]byte("must not overwrite")), IfMatch: aws.String(`"flowdesk-intentionally-wrong-etag"`)})
	var apiError smithy.APIError
	if !errors.As(err, &apiError) || apiError.ErrorCode() != "PreconditionFailed" {
		t.Fatal("provider did not enforce mismatched If-Match", err)
	}
	if received, readErr := source.Read(ctx, key, int64(len(raw))); readErr != nil || !bytes.Equal(received, raw) {
		t.Fatal("conditional rejection changed source", readErr)
	}
	if err = source.Retire(ctx, key, hash, int64(len(raw)), id); err != nil {
		t.Fatal(err)
	}
	if err = source.PutIfAbsent(ctx, key, raw); err != nil {
		t.Fatal(err)
	}
	if received, readErr := source.Read(ctx, key, int64(len(raw))); readErr != nil || len(received) != 0 {
		t.Fatal("late PutIfAbsent resurrected source", readErr)
	}
	if err = source.Retire(ctx, key, hash, int64(len(raw)), id); err != nil {
		t.Fatal("marker replay", err)
	}
	legacy := prefix + hash + ".pdf"
	if err = source.PutIfAbsent(ctx, legacy, raw); err != nil {
		t.Fatal(err)
	}
	encoded := prefix + "z%2F+文件.pdf"
	if _, err = source.api.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(source.bucket), Key: aws.String(encoded), Body: bytes.NewReader(raw), IfNoneMatch: aws.String("*")}); err != nil {
		t.Fatal(err)
	}
	first, err := source.ListVersionObjects(ctx, tenant, id, "", 1)
	if err != nil || len(first.Items) != 1 || !first.HasMore || first.Items[0].Key != legacy || first.Items[0].Size != int64(len(raw)) {
		t.Fatal("legacy inventory", err)
	}
	next, err := source.ListVersionObjects(ctx, tenant, id, first.Items[0].Key, 1)
	if err != nil || len(next.Items) != 1 || !next.HasMore || next.Items[0].Key != key || next.Items[0].Size != 0 {
		t.Fatal("zero-byte inventory", err)
	}
	last, err := source.ListVersionObjects(ctx, tenant, id, next.Items[0].Key, 1)
	if err != nil || len(last.Items) != 1 || last.HasMore || last.Items[0].Key != encoded {
		t.Fatal("encoded inventory", err)
	}
	// SeaweedFS 4.52 encodes spaces as raw '+' in listing responses. The
	// ambiguity must fail closed, rather than fingerprint a different key.
	if _, err = source.api.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(source.bucket), Key: aws.String(prefix + "zz space.pdf"), Body: bytes.NewReader(raw), IfNoneMatch: aws.String("*")}); err != nil {
		t.Fatal(err)
	}
	if _, err = source.ListVersionObjects(ctx, tenant, id, encoded, 1); err != ErrSourceUnavailable {
		t.Fatal("ambiguous provider encoding accepted", err)
	}
}

func TestS3RetirementConditionalMarkerAndLateUpload(t *testing.T) {
	raw := []byte("%PDF synthetic source cleanup")
	digest := sha256.Sum256(raw)
	hash := hex.EncodeToString(digest[:])
	key := "knowledge/11111111-1111-1111-1111-111111111111/22222222-2222-2222-2222-222222222222/" + hash + "/0.pdf"
	id := "33333333-3333-3333-3333-333333333333"
	for _, scenario := range []string{"existing", "missing", "lost_response", "etag_conflict", "corrupt", "foreign_marker", "enabled", "suspended", "denied"} {
		t.Run(scenario, func(t *testing.T) {
			exists := scenario != "missing"
			content := append([]byte(nil), raw...)
			metadata := map[string]string{}
			etag := `"opaque-original"`
			puts := 0
			if scenario == "corrupt" {
				content = []byte("SYNTHETIC CORRUPTION")
			}
			if scenario == "foreign_marker" {
				content = []byte{}
				metadata["flowdesk-retired"] = "1"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.Contains(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256") {
					t.Error("unsigned S3 request")
				}
				if r.URL.Query().Has("versioning") {
					if scenario == "denied" {
						w.WriteHeader(403)
						io.WriteString(w, "<Error><Code>AccessDenied</Code></Error>")
						return
					}
					status := ""
					if scenario == "enabled" {
						status = "<Status>Enabled</Status>"
					}
					if scenario == "suspended" {
						status = "<Status>Suspended</Status>"
					}
					io.WriteString(w, `<VersioningConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/">`+status+"</VersioningConfiguration>")
					return
				}
				if r.URL.Path != "/private-pdfs/"+key {
					t.Error("unsafe target")
				}
				if r.Method == http.MethodGet {
					if !exists {
						w.WriteHeader(404)
						io.WriteString(w, "<Error><Code>NoSuchKey</Code></Error>")
						return
					}
					w.Header().Set("ETag", etag)
					for k, v := range metadata {
						w.Header().Set("X-Amz-Meta-"+k, v)
					}
					w.Write(content)
					return
				}
				if r.Method != http.MethodPut {
					t.Error("unexpected destructive method")
					w.WriteHeader(405)
					return
				}
				puts++
				if r.Header.Get("If-None-Match") == "*" && exists {
					w.WriteHeader(412)
					io.WriteString(w, "<Error><Code>PreconditionFailed</Code></Error>")
					return
				}
				if scenario == "etag_conflict" && puts == 1 {
					etag = `"opaque-changed"`
					w.WriteHeader(412)
					io.WriteString(w, "<Error><Code>PreconditionFailed</Code></Error>")
					return
				}
				if exists && r.Header.Get("If-Match") != etag {
					t.Error("unconditional marker overwrite")
					w.WriteHeader(412)
					return
				}
				content, _ = io.ReadAll(r.Body)
				exists = true
				etag = `"opaque-marker"`
				for _, k := range []string{"flowdesk-retired", "flowdesk-object-id", "flowdesk-content-hash"} {
					metadata[k] = r.Header.Get("X-Amz-Meta-" + k)
				}
				if scenario == "lost_response" && puts == 1 {
					connection, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
					} else {
						connection.Close()
					}
					return
				}
			}))
			defer server.Close()
			store, err := NewS3SourceStore(S3SourceConfig{Endpoint: server.URL, Bucket: "private-pdfs", AccessKey: "synthetic-access", SecretKey: "synthetic-secret", AllowHTTP: true})
			if err != nil {
				t.Fatal(err)
			}
			cleaner := store.(SourceRetirer)
			err = cleaner.Retire(context.Background(), key, hash, int64(len(raw)), id)
			switch scenario {
			case "enabled", "suspended", "denied":
				if !errors.Is(err, ErrSourceCleanupUnsupported) || puts != 0 {
					t.Fatal("unsupported storage mutated", err, puts)
				}
				return
			case "corrupt", "foreign_marker":
				if !errors.Is(err, ErrSourceIntegrity) || puts != 0 {
					t.Fatal("unverified source mutated", err, puts)
				}
				return
			case "lost_response":
				if err == nil {
					t.Fatal("lost response not surfaced")
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
			}
			if err = cleaner.Retire(context.Background(), key, hash, int64(len(raw)), id); err != nil {
				t.Fatal("marker replay", err)
			}
			before := puts
			if err = store.PutIfAbsent(context.Background(), key, raw); err != nil {
				t.Fatal(err)
			}
			if len(content) != 0 || metadata["flowdesk-object-id"] != id || puts != before+1 {
				t.Fatal("late upload resurrected original")
			}
		})
	}
}
