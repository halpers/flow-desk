package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestS3ConditionalWriteBoundedReadAndSafeErrors(t *testing.T) {
	key := "knowledge/11111111-1111-1111-1111-111111111111/22222222-2222-2222-2222-222222222222/" + strings.Repeat("a", 64) + ".pdf"
	raw := "%PDF-1.7 synthetic"
	stored := ""
	puts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/private-pdfs/"+key || !strings.Contains(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256") {
			t.Error("unsigned request or unsafe path")
		}
		if r.Method == "PUT" {
			puts++
			if r.Header.Get("If-None-Match") != "*" {
				t.Error("missing conditional write")
			}
			if stored != "" {
				w.WriteHeader(412)
				_, _ = io.WriteString(w, "<Error><Code>PreconditionFailed</Code></Error>")
				return
			}
			body, _ := io.ReadAll(r.Body)
			stored = string(body)
			w.WriteHeader(200)
			return
		}
		if stored == "" {
			w.WriteHeader(404)
			_, _ = io.WriteString(w, "<Error><Code>NoSuchKey</Code><Message>synthetic-private-details</Message></Error>")
			return
		}
		_, _ = io.WriteString(w, stored)
	}))
	defer server.Close()
	config := S3SourceConfig{Endpoint: server.URL, Bucket: "private-pdfs", Region: "us-east-1", AccessKey: "synthetic-access", SecretKey: "synthetic-secret", AllowHTTP: true}
	store, err := NewS3SourceStore(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Read(context.Background(), key, 100); !errors.Is(err, ErrSourceMissing) {
		t.Fatal("missing mapping", err)
	}
	if err = store.PutIfAbsent(context.Background(), key, []byte(raw)); err != nil {
		t.Fatal(err)
	}
	if err = store.PutIfAbsent(context.Background(), key, []byte("different")); err != nil {
		t.Fatal(err)
	}
	got, err := store.Read(context.Background(), key, 100)
	if err != nil || string(got) != raw || puts != 2 {
		t.Fatal("conditional write/read", err)
	}
	if _, err = store.Read(context.Background(), key, 3); !errors.Is(err, ErrSourceUnavailable) {
		t.Fatal("unbounded read")
	}
	if _, err = store.Read(context.Background(), "https://untrusted/secret", 100); !errors.Is(err, ErrSourceUnavailable) {
		t.Fatal("accepted external key")
	}
	config.AllowHTTP = false
	if _, err = NewS3SourceStore(config); err == nil {
		t.Fatal("HTTP allowed without opt-in")
	}
	config.Endpoint = "https://user:secret@example.test"
	if _, err = NewS3SourceStore(config); err == nil {
		t.Fatal("URL credential accepted")
	}
}
