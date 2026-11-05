package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/google/uuid"
)

var ErrSourceCleanupUnsupported = errors.New("source cleanup unsupported")
var ErrSourceIntegrity = errors.New("source integrity failed")

// SourceRetirer retains a zero-byte object fence so late conditional uploads cannot resurrect it.
type SourceRetirer interface {
	CheckRetirement(context.Context) error
	Retire(context.Context, string, string, int64, string) error
}

const sourceRetirementAttempts = 3

func (s *s3SourceStore) CheckRetirement(ctx context.Context) error {
	versioning, err := s.api.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{Bucket: aws.String(s.bucket)})
	if err != nil || versioning.Status != "" {
		return ErrSourceCleanupUnsupported
	}
	return nil
}

func (s *s3SourceStore) Retire(ctx context.Context, key, hash string, size int64, objectID string) error {
	if !sourceKeyPattern.MatchString(key) || len(hash) != 64 || size < 1 || size > 2*1024*1024 || (!strings.Contains(key, "/"+hash+"/") && !strings.HasSuffix(key, "/"+hash+".pdf")) {
		return ErrSourceUnavailable
	}
	if _, err := uuid.Parse(objectID); err != nil {
		return ErrSourceUnavailable
	}
	if err := s.CheckRetirement(ctx); err != nil {
		return err
	}
	for range sourceRetirementAttempts {
		result, err := s.api.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
		missing := false
		if err != nil {
			var apiError smithy.APIError
			if !errors.As(err, &apiError) || (apiError.ErrorCode() != "NoSuchKey" && apiError.ErrorCode() != "NotFound") {
				return ErrSourceUnavailable
			}
			missing = true
		}
		request := &s3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key), Body: bytes.NewReader(nil), ContentType: aws.String("application/x-flowdesk-retired"), Metadata: map[string]string{"flowdesk-retired": "1", "flowdesk-object-id": objectID, "flowdesk-content-hash": hash}}
		if missing {
			request.IfNoneMatch = aws.String("*")
		} else {
			raw, readErr := io.ReadAll(io.LimitReader(result.Body, size+1))
			result.Body.Close()
			if readErr != nil {
				return ErrSourceUnavailable
			}
			if aws.ToString(result.VersionId) != "" && aws.ToString(result.VersionId) != "null" {
				return ErrSourceCleanupUnsupported
			}
			if len(raw) == 0 && result.Metadata["flowdesk-retired"] == "1" && result.Metadata["flowdesk-object-id"] == objectID && result.Metadata["flowdesk-content-hash"] == hash {
				return nil
			}
			digest := sha256.Sum256(raw)
			if int64(len(raw)) != size || hex.EncodeToString(digest[:]) != hash {
				return ErrSourceIntegrity
			}
			if aws.ToString(result.ETag) == "" {
				return ErrSourceCleanupUnsupported
			}
			request.IfMatch = result.ETag
		}
		_, err = s.api.PutObject(ctx, request)
		if err == nil {
			continue
		} // Read back the marker before acknowledging an irreversible operation.
		var apiError smithy.APIError
		if errors.As(err, &apiError) && (apiError.ErrorCode() == "PreconditionFailed" || apiError.ErrorCode() == "ConditionalRequestConflict") {
			continue
		}
		return ErrSourceUnavailable
	}
	return ErrSourceUnavailable
}
