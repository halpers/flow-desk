package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestSourceInventoryScopedEncodedPaginationAndFailures(t *testing.T) {
	tenant, version := "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	prefix := "knowledge/" + tenant + "/" + version + "/"
	objectXML := func(key, size string) string {
		return "<Contents><Key>" + strings.ReplaceAll(url.QueryEscape(key), "+", "%20") + "</Key><Size>" + size + "</Size><LastModified>2026-11-05T00:00:00Z</LastModified></Contents>"
	}
	for _, scenario := range []string{"page", "encoded", "ambiguous_plus", "outside", "duplicate", "unordered", "too_many", "negative_size", "huge_size", "long_key", "empty_truncated", "missing_truncated", "missing_modified", "invalid_xml", "large_xml", "denied", "redirect"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || strings.TrimRight(r.URL.Path, "/") != "/private-pdfs" || !strings.Contains(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256") || r.URL.Query().Get("prefix") != prefix || r.URL.Query().Get("max-keys") != "3" || r.URL.Query().Get("encoding-type") != "url" {
					t.Error("unsafe or unsigned inventory request", r.Method)
				}
				if scenario == "denied" {
					w.WriteHeader(403)
					io.WriteString(w, "<Error><Code>AccessDenied</Code><Message>private-diagnostic</Message></Error>")
					return
				}
				if scenario == "redirect" {
					w.Header().Set("Location", "http://127.0.0.1:1/private")
					w.WriteHeader(302)
					return
				}
				if scenario == "invalid_xml" {
					io.WriteString(w, "not XML private-diagnostic")
					return
				}
				if scenario == "large_xml" {
					io.WriteString(w, "<ListBucketResult><Contents><Key>"+strings.Repeat("a", int(maximumInventoryResponseBytes)+1024))
					return
				}
				body, truncated := objectXML(prefix+"a.pdf", "7")+objectXML(prefix+"b.pdf", "8")+objectXML(prefix+"c.pdf", "9"), "false"
				switch scenario {
				case "page":
					if calls == 2 {
						if r.URL.Query().Get("start-after") != prefix+"b.pdf" {
							t.Error("cursor start-after")
						}
						body = objectXML(prefix+"c.pdf", "9")
					}
				case "encoded":
					body = objectXML(prefix+"odd%2F+ 文件.pdf", "7")
				case "ambiguous_plus":
					body = "<Contents><Key>" + url.PathEscape(prefix) + "ambiguous+name.pdf</Key><Size>7</Size><LastModified>2026-11-05T00:00:00Z</LastModified></Contents>"
				case "outside":
					body = objectXML("knowledge/foreign/private.pdf", "7")
				case "duplicate":
					body = objectXML(prefix+"a.pdf", "7") + objectXML(prefix+"a.pdf", "7")
				case "unordered":
					body = objectXML(prefix+"b.pdf", "7") + objectXML(prefix+"a.pdf", "7")
				case "too_many":
					body += objectXML(prefix+"d.pdf", "7")
				case "negative_size":
					body = objectXML(prefix+"a.pdf", "-1")
				case "huge_size":
					body = objectXML(prefix+"a.pdf", "9007199254740992")
				case "long_key":
					body = objectXML(prefix+strings.Repeat("a", 1024), "7")
				case "empty_truncated":
					body = ""
					truncated = "true"
				case "missing_modified":
					body = "<Contents><Key>" + url.PathEscape(prefix+"a.pdf") + "</Key><Size>7</Size></Contents>"
				}
				marker := "<IsTruncated>" + truncated + "</IsTruncated>"
				if scenario == "missing_truncated" {
					marker = ""
				}
				io.WriteString(w, "<ListBucketResult><Name>private-pdfs</Name><EncodingType>url</EncodingType>"+marker+body+"</ListBucketResult>")
			}))
			defer server.Close()
			source, err := NewS3SourceStore(S3SourceConfig{Endpoint: server.URL, Bucket: "private-pdfs", Region: "us-east-1", AccessKey: "synthetic-access", SecretKey: "synthetic-secret", AllowHTTP: true})
			if err != nil {
				t.Fatal(err)
			}
			inventory := source.(SourceInventory)
			page, err := inventory.ListVersionObjects(context.Background(), tenant, version, "", 2)
			if scenario != "page" && scenario != "encoded" {
				if err != ErrSourceUnavailable || calls != 1 {
					t.Fatal("unsafe response accepted or error leaked", err, calls)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "encoded" {
				if len(page.Items) != 1 || page.Items[0].Key != prefix+"odd%2F+ 文件.pdf" {
					t.Fatal("encoded key altered", page)
				}
				return
			}
			if len(page.Items) != 2 || !page.HasMore {
				t.Fatal("lookahead", page)
			}
			next, err := inventory.ListVersionObjects(context.Background(), tenant, version, page.Items[1].Key, 2)
			if err != nil || len(next.Items) != 1 || next.HasMore || next.Items[0].Key != prefix+"c.pdf" {
				t.Fatal("next page", next, err)
			}
			for _, input := range []struct {
				tenant, version, after string
				limit                  int32
			}{
				{"bad", version, "", 2}, {tenant, "bad", "", 2}, {tenant, version, "knowledge/foreign/key", 2}, {tenant, version, "", 0}, {tenant, version, "", 51},
			} {
				if _, err = inventory.ListVersionObjects(context.Background(), input.tenant, input.version, input.after, input.limit); err == nil {
					t.Fatal(fmt.Sprintf("unsafe input reached storage: %+v", input))
				}
			}
			if calls != 2 {
				t.Fatal("invalid input reached storage")
			}
		})
	}
}
