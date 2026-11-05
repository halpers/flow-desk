package client

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"
)

const maximumInventoryPage int32 = 50
const maximumInventoryKeyBytes = 1024
const maximumInventoryResponseBytes int64 = 256 * 1024
const maximumInventoryJSONInteger int64 = 9007199254740991

type SourceObjectMetadata struct {
	Key        string
	Size       int64
	ModifiedAt time.Time
}
type SourceObjectPage struct {
	Items   []SourceObjectMetadata
	HasMore bool
}
type SourceInventory interface {
	ListVersionObjects(context.Context, string, string, string, int32) (SourceObjectPage, error)
}

type inventoryHTTPClient struct{ base aws.HTTPClient }
type inventoryResponseBody struct {
	io.Reader
	io.Closer
}

func (c inventoryHTTPClient) Do(request *http.Request) (*http.Response, error) {
	response, err := c.base.Do(request)
	if err == nil && response.Body != nil {
		response.Body = inventoryResponseBody{io.LimitReader(response.Body, maximumInventoryResponseBytes), response.Body}
	}
	return response, err
}

func (s *s3SourceStore) ListVersionObjects(ctx context.Context, tenant, version, after string, limit int32) (SourceObjectPage, error) {
	page := SourceObjectPage{Items: []SourceObjectMetadata{}}
	for _, id := range []string{tenant, version} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed.String() != id {
			return page, ErrSourceUnavailable
		}
	}
	prefix := "knowledge/" + tenant + "/" + version + "/"
	if limit < 1 || limit > maximumInventoryPage || (after != "" && (!strings.HasPrefix(after, prefix) || len(after) > maximumInventoryKeyBytes || !utf8.ValidString(after))) {
		return page, ErrSourceUnavailable
	}
	input := &s3.ListObjectsV2Input{Bucket: aws.String(s.bucket), Prefix: aws.String(prefix), MaxKeys: aws.Int32(limit + 1), EncodingType: types.EncodingTypeUrl}
	if after != "" {
		input.StartAfter = aws.String(after)
	}
	result, err := s.api.ListObjectsV2(ctx, input, func(options *s3.Options) { options.HTTPClient = inventoryHTTPClient{options.HTTPClient} })
	if err != nil {
		return page, ErrSourceUnavailable
	}
	if result.IsTruncated == nil || len(result.Contents) > int(limit+1) || len(result.CommonPrefixes) > 0 ||
		(result.EncodingType != "" && result.EncodingType != types.EncodingTypeUrl) || (result.Name != nil && *result.Name != s.bucket) {
		return page, ErrSourceUnavailable
	}
	previous := after
	for _, object := range result.Contents {
		key := aws.ToString(object.Key)
		if result.EncodingType == types.EncodingTypeUrl {
			// A raw plus can mean a literal '+' or a form-encoded space on some
			// compatible stores. Never reconcile an ambiguous object identity.
			if strings.Contains(key, "+") {
				return page, ErrSourceUnavailable
			}
			key, err = url.PathUnescape(key)
			if err != nil {
				return page, ErrSourceUnavailable
			}
		}
		if !strings.HasPrefix(key, prefix) || len(key) <= len(prefix) || len(key) > maximumInventoryKeyBytes || !utf8.ValidString(key) || key <= previous ||
			object.Size == nil || *object.Size < 0 || *object.Size > maximumInventoryJSONInteger || object.LastModified == nil {
			return page, ErrSourceUnavailable
		}
		page.Items = append(page.Items, SourceObjectMetadata{Key: key, Size: *object.Size, ModifiedAt: *object.LastModified})
		previous = key
	}
	page.HasMore = aws.ToBool(result.IsTruncated) || len(page.Items) > int(limit)
	if page.HasMore && len(page.Items) == 0 {
		return page, ErrSourceUnavailable
	}
	if len(page.Items) > int(limit) {
		page.Items = page.Items[:limit]
	}
	return page, nil
}
