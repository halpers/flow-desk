package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

var ErrSourceMissing = errors.New("source missing")
var ErrSourceUnavailable = errors.New("source unavailable")
var sourceKeyPattern = regexp.MustCompile(`^knowledge/[0-9a-f-]{36}/[0-9a-f-]{36}/[0-9a-f]{64}(\.pdf|/[0-9]{1,19}\.pdf)$`)
var bucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)

type SourceStore interface {
	ID() string
	Read(context.Context, string, int64) ([]byte, error)
	PutIfAbsent(context.Context, string, []byte) error
}
type S3SourceConfig struct {
	Endpoint, Region, Bucket, AccessKey, SecretKey string
	AllowHTTP                                      bool
}
type s3SourceStore struct {
	api        *s3.Client
	bucket, id string
}

func NewS3SourceStore(config S3SourceConfig) (SourceStore, error) {
	endpoint, err := url.Parse(config.Endpoint)
	if err != nil || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || (endpoint.Path != "" && endpoint.Path != "/") || (endpoint.Scheme != "https" && !(endpoint.Scheme == "http" && config.AllowHTTP)) || !bucketPattern.MatchString(config.Bucket) || strings.Contains(config.Bucket, "..") || config.AccessKey == "" || config.SecretKey == "" {
		return nil, ErrSourceUnavailable
	}
	if config.Region == "" {
		config.Region = "us-east-1"
	}
	base := strings.TrimRight(endpoint.String(), "/")
	fingerprint := sha256.Sum256([]byte(base + "\n" + config.Region + "\n" + config.Bucket))
	httpClient := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{MaxIdleConns: 10, IdleConnTimeout: 30 * time.Second}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	api := s3.New(s3.Options{Region: config.Region, BaseEndpoint: aws.String(base), UsePathStyle: true,
		Credentials: credentials.NewStaticCredentialsProvider(config.AccessKey, config.SecretKey, ""), HTTPClient: httpClient, RetryMaxAttempts: 1,
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired, ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired})
	return &s3SourceStore{api: api, bucket: config.Bucket, id: hex.EncodeToString(fingerprint[:])}, nil
}
func (s *s3SourceStore) ID() string { return s.id }
func (s *s3SourceStore) Read(ctx context.Context, key string, limit int64) ([]byte, error) {
	if !sourceKeyPattern.MatchString(key) || limit < 1 || limit > 2*1024*1024 {
		return nil, ErrSourceUnavailable
	}
	result, err := s.api.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		var apiError smithy.APIError
		if errors.As(err, &apiError) && (apiError.ErrorCode() == "NoSuchKey" || apiError.ErrorCode() == "NotFound") {
			return nil, ErrSourceMissing
		}
		return nil, ErrSourceUnavailable
	}
	defer result.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(result.Body, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, ErrSourceUnavailable
	}
	return raw, nil
}
func (s *s3SourceStore) PutIfAbsent(ctx context.Context, key string, raw []byte) error {
	if !sourceKeyPattern.MatchString(key) || len(raw) == 0 || len(raw) > 2*1024*1024 {
		return ErrSourceUnavailable
	}
	_, err := s.api.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key), Body: bytes.NewReader(raw), ContentType: aws.String("application/pdf"), IfNoneMatch: aws.String("*")})
	if err != nil {
		var apiError smithy.APIError
		if errors.As(err, &apiError) && (apiError.ErrorCode() == "PreconditionFailed" || apiError.ErrorCode() == "ConditionalRequestConflict") {
			return nil
		}
		return ErrSourceUnavailable
	}
	return nil
}
