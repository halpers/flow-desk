package client

import (
	"bytes"
	"context"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestTenantSourceInventoryUsesOnlyCanonicalTenantPrefix(t *testing.T) {
	tenant := "11111111-1111-1111-1111-111111111111"
	prefix := "knowledge/" + tenant + "/"
	calls := 0
	outside := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Query().Get("prefix") != prefix || r.URL.Query().Get("max-keys") != "2" || !strings.Contains(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256") {
			t.Error("unsafe listing")
		}
		key := prefix + "unknown.txt"
		if outside {
			key = "knowledge/22222222-2222-2222-2222-222222222222/private.txt"
		}
		io.WriteString(w, "<ListBucketResult><Name>private-pdfs</Name><IsTruncated>false</IsTruncated><Contents><Key>"+key+"</Key><Size>1</Size><LastModified>2026-11-06T00:00:00Z</LastModified></Contents></ListBucketResult>")
	}))
	defer server.Close()
	source, err := NewS3SourceStore(S3SourceConfig{Endpoint: server.URL, Bucket: "private-pdfs", Region: "us-east-1", AccessKey: "synthetic-access", SecretKey: "synthetic-secret", AllowHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	inspector := source.(SourceTenantInventory)
	page, err := inspector.ListTenantObjects(context.Background(), tenant, "", 1)
	if err != nil || len(page.Items) != 1 || page.Items[0].Key != prefix+"unknown.txt" {
		t.Fatal(page, err)
	}
	outside = true
	if _, err = inspector.ListTenantObjects(context.Background(), tenant, "", 1); err != ErrSourceUnavailable {
		t.Fatal("outside scope accepted", err)
	}
	for _, value := range []string{"bad", strings.ToUpper("abcdefab-1111-1111-1111-111111111111"), "../" + tenant} {
		if _, err = inspector.ListTenantObjects(context.Background(), value, "", 1); err != ErrSourceUnavailable {
			t.Fatal("unsafe tenant", err)
		}
	}
	if _, err = inspector.ListTenantObjects(context.Background(), tenant, "knowledge/foreign/key", 1); err != ErrSourceUnavailable {
		t.Fatal("unsafe after")
	}
	if calls != 2 {
		t.Fatal("invalid input reached store")
	}
}

// Only a disposable mock stack may use this test to construct synthetic observations.
func TestS3SandboxTenantInventoryFixture(t *testing.T) {
	if os.Getenv("FLOWDESK_S3_SANDBOX_TESTS") != "1" {
		t.Skip("disposable S3 fixture only")
	}
	if os.Getenv("MODEL_PROVIDER") != "mock" {
		t.Fatal("mock required")
	}
	tenant, version, hash := os.Getenv("FLOWDESK_S3_TEST_TENANT"), os.Getenv("FLOWDESK_S3_TEST_VERSION"), os.Getenv("FLOWDESK_S3_TEST_HASH")
	if _, err := uuid.Parse(tenant); err != nil {
		t.Fatal("synthetic tenant required")
	}
	if _, err := uuid.Parse(version); err != nil {
		t.Fatal("synthetic version required")
	}
	if len(hash) != 64 {
		t.Fatal("synthetic hash required")
	}
	source, err := NewS3SourceStore(S3SourceConfig{Endpoint: os.Getenv("FILE_STORE_ENDPOINT"), Region: os.Getenv("FILE_STORE_REGION"), Bucket: os.Getenv("FILE_STORE_BUCKET"), AccessKey: os.Getenv("FILE_STORE_ACCESS_KEY"), SecretKey: os.Getenv("FILE_STORE_SECRET_KEY"), AllowHTTP: os.Getenv("FILE_STORE_ALLOW_HTTP") == "true"})
	if err != nil {
		t.Fatal("source configuration")
	}
	store := source.(*s3SourceStore)
	prefix := "knowledge/" + tenant + "/"
	unknown := uuid.NewString()
	keys := []string{prefix + "misc/nonstandard.txt", prefix + version + "/" + strings.Repeat("a", 64) + ".pdf", "knowledge/" + uuid.NewString() + "/" + unknown + "/" + hash + ".pdf"}
	for epoch := range 22 {
		keys = append(keys, fmt.Sprintf("%s%s/%s/%d.pdf", prefix, unknown, hash, epoch))
	}
	for _, key := range keys {
		_, err = store.api.PutObject(context.Background(), &s3.PutObjectInput{Bucket: aws.String(store.bucket), Key: aws.String(key), Body: bytes.NewReader([]byte("synthetic inventory fixture")), IfNoneMatch: aws.String("*")})
		if err != nil {
			t.Fatal("synthetic fixture write failed")
		}
	}
	page, err := store.ListTenantObjects(context.Background(), tenant, "", 50)
	if err != nil || len(page.Items) != 25 || page.HasMore {
		t.Fatal("scoped fixture probe", len(page.Items), err)
	}
}
