package backend

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // S3 Content-MD5 requires MD5 for transport integrity checks.
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

type S3Client interface {
	PutObject(context.Context, *awss3.PutObjectInput, ...func(*awss3.Options)) (*awss3.PutObjectOutput, error)
	HeadObject(context.Context, *awss3.HeadObjectInput, ...func(*awss3.Options)) (*awss3.HeadObjectOutput, error)
	GetObject(context.Context, *awss3.GetObjectInput, ...func(*awss3.Options)) (*awss3.GetObjectOutput, error)
	DeleteObject(context.Context, *awss3.DeleteObjectInput, ...func(*awss3.Options)) (*awss3.DeleteObjectOutput, error)
	ListObjectsV2(context.Context, *awss3.ListObjectsV2Input, ...func(*awss3.Options)) (*awss3.ListObjectsV2Output, error)
}

type S3Config struct {
	Bucket    string
	Region    string
	Endpoint  string
	PathStyle bool
}

type s3Backend struct {
	bucket string
	client S3Client
}

func NewS3(client S3Client, bucket string) Backend {
	return &s3Backend{
		bucket: bucket,
		client: client,
	}
}

func NewS3FromConfig(ctx context.Context, cfg S3Config) (Backend, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.Region))
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	client := awss3.NewFromConfig(awsCfg, func(o *awss3.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
		o.UsePathStyle = cfg.PathStyle
	})
	return NewS3(client, cfg.Bucket), nil
}

func ParseS3ConfigFromEnv() (S3Config, error) {
	cfg := S3Config{
		Bucket:   os.Getenv("SCRAP_S3_BUCKET"),
		Region:   os.Getenv("SCRAP_S3_REGION"),
		Endpoint: os.Getenv("SCRAP_S3_ENDPOINT"),
	}

	pathStyle := os.Getenv("SCRAP_S3_PATH_STYLE")
	if pathStyle != "" {
		parsed, err := strconv.ParseBool(pathStyle)
		if err != nil {
			return S3Config{}, fmt.Errorf("invalid SCRAP_S3_PATH_STYLE %q: %w: %w", pathStyle, ErrPermanent, err)
		}
		cfg.PathStyle = parsed
	}
	if err := cfg.validate(); err != nil {
		return S3Config{}, err
	}
	return cfg, nil
}

func (cfg S3Config) validate() error {
	if cfg.Bucket == "" {
		return fmt.Errorf("SCRAP_S3_BUCKET is required: %w", ErrPermanent)
	}
	if cfg.Region == "" {
		return fmt.Errorf("SCRAP_S3_REGION is required: %w", ErrPermanent)
	}
	return nil
}

func (b *s3Backend) PutObject(ctx context.Context, key string, body io.Reader, size int64, _ PutOpts) (PutResult, error) {
	if err := b.validateRequest("put object", key); err != nil {
		return PutResult{}, err
	}
	payload, md5Hex, md5Base64, err := readPutBody(body, size)
	if err != nil {
		return PutResult{}, err
	}

	out, err := b.client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket:        aws.String(b.bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(payload),
		ContentLength: aws.Int64(size),
		ContentMD5:    aws.String(md5Base64),
		ContentType:   aws.String(DefaultContentType),
	})
	if err != nil {
		return PutResult{}, classifyS3Error("put object", err)
	}
	etag, err := verifiedS3ETag(out.ETag, md5Hex)
	if err != nil {
		return PutResult{}, fmt.Errorf("put object: %w", err)
	}
	return PutResult{ETag: etag, Size: size}, nil
}

func (b *s3Backend) HeadObject(ctx context.Context, key string) (ObjectMeta, error) {
	if err := b.validateRequest("head object", key); err != nil {
		return ObjectMeta{}, err
	}
	out, err := b.client.HeadObject(ctx, &awss3.HeadObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return ObjectMeta{}, classifyS3Error("head object", err)
	}
	meta, err := objectMetaFromS3Head(out)
	if err != nil {
		return ObjectMeta{}, fmt.Errorf("head object: %w", err)
	}
	return meta, nil
}

func (b *s3Backend) GetObject(ctx context.Context, key string, opts GetOpts) (io.ReadCloser, ObjectMeta, error) {
	if err := b.validateRequest("get object", key); err != nil {
		return nil, ObjectMeta{}, err
	}
	meta, err := b.HeadObject(ctx, key)
	if err != nil {
		return nil, ObjectMeta{}, err
	}

	rangeHeader, err := s3RangeHeader(opts.Range)
	if err != nil {
		return nil, ObjectMeta{}, err
	}
	if opts.Range.Enabled && opts.Range.Length == 0 {
		return io.NopCloser(bytes.NewReader(nil)), meta, nil
	}

	input := &awss3.GetObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(key),
	}
	if rangeHeader != "" {
		input.Range = aws.String(rangeHeader)
	}
	out, err := b.client.GetObject(ctx, input)
	if err != nil {
		return nil, ObjectMeta{}, classifyS3Error("get object", err)
	}
	if out.Body == nil {
		return nil, ObjectMeta{}, fmt.Errorf("get object: empty response body: %w", ErrCorrupt)
	}
	return out.Body, meta, nil
}

func (b *s3Backend) DeleteObject(ctx context.Context, key string) error {
	if err := b.validateRequest("delete object", key); err != nil {
		return err
	}
	_, err := b.client.DeleteObject(ctx, &awss3.DeleteObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return classifyS3Error("delete object", err)
	}
	return nil
}

func (b *s3Backend) ListObjects(ctx context.Context, prefix string, _ ListOpts) (ObjectIterator, error) {
	if b.bucket == "" {
		return nil, fmt.Errorf("list objects: S3 bucket is empty: %w", ErrPermanent)
	}
	if err := validatePrefix(prefix); err != nil {
		return nil, err
	}

	paginator := awss3.NewListObjectsV2Paginator(b.client, &awss3.ListObjectsV2Input{
		Bucket: aws.String(b.bucket),
		Prefix: aws.String(prefix),
	})
	return &s3PaginatedIterator{ctx: ctx, paginator: paginator}, nil
}

func (b *s3Backend) validateRequest(op, key string) error {
	if b.bucket == "" {
		return fmt.Errorf("%s: S3 bucket is empty: %w", op, ErrPermanent)
	}
	if err := validateKey(key); err != nil {
		return err
	}
	return nil
}

func readPutBody(body io.Reader, size int64) ([]byte, string, string, error) {
	if size < 0 {
		return nil, "", "", fmt.Errorf("put object: negative size %d: %w", size, ErrPermanent)
	}

	var buf bytes.Buffer
	hash := md5.New() //nolint:gosec // S3 Content-MD5 is required for transport integrity, not password hashing.
	// Bound the copy to size+1 so an over-long body is rejected as ErrCorrupt
	// without buffering past the declared size — matching the FS backend.
	limit := size + 1
	if size == math.MaxInt64 {
		limit = size
	}
	written, err := io.Copy(io.MultiWriter(&buf, hash), io.LimitReader(body, limit))
	if err != nil {
		return nil, "", "", fmt.Errorf("put object: read body: %w: %w", ErrTransient, err)
	}
	if written != size {
		return nil, "", "", fmt.Errorf("put object: copied %d bytes, expected %d: %w", written, size, ErrCorrupt)
	}

	sum := hash.Sum(nil)
	return buf.Bytes(), hex.EncodeToString(sum), base64.StdEncoding.EncodeToString(sum), nil
}

func objectMetaFromS3Head(out *awss3.HeadObjectOutput) (ObjectMeta, error) {
	if out == nil || out.ContentLength == nil {
		return ObjectMeta{}, fmt.Errorf("missing content length: %w", ErrCorrupt)
	}
	if *out.ContentLength < 0 {
		return ObjectMeta{}, fmt.Errorf("negative content length %d: %w", *out.ContentLength, ErrCorrupt)
	}

	contentType := aws.ToString(out.ContentType)
	if contentType == "" {
		contentType = DefaultContentType
	}
	return ObjectMeta{
		ETag:        normalizeS3ETag(out.ETag),
		Size:        *out.ContentLength,
		ContentType: contentType,
	}, nil
}

func objectInfoFromS3(obj types.Object) (ObjectInfo, error) {
	if obj.Key == nil || *obj.Key == "" {
		return ObjectInfo{}, fmt.Errorf("object key is empty: %w", ErrCorrupt)
	}
	if obj.Size == nil || *obj.Size < 0 {
		return ObjectInfo{}, fmt.Errorf("object %q has invalid size: %w", *obj.Key, ErrCorrupt)
	}
	return ObjectInfo{
		Key:         *obj.Key,
		ETag:        normalizeS3ETag(obj.ETag),
		Size:        *obj.Size,
		ContentType: DefaultContentType,
	}, nil
}

func verifiedS3ETag(raw *string, wantMD5Hex string) (string, error) {
	etag := normalizeS3ETag(raw)
	if !isMD5Hex(etag) {
		return etag, nil
	}
	if etag != strings.ToLower(wantMD5Hex) {
		return "", fmt.Errorf("etag %q did not match MD5 %q: %w", etag, wantMD5Hex, ErrCorrupt)
	}
	return etag, nil
}

func normalizeS3ETag(raw *string) string {
	return strings.ToLower(strings.Trim(aws.ToString(raw), `"`))
}

func isMD5Hex(etag string) bool {
	if len(etag) != md5HexLen {
		return false
	}
	_, err := hex.DecodeString(etag)
	return err == nil
}

const md5HexLen = 32

func s3RangeHeader(byteRange ByteRange) (string, error) {
	if !byteRange.Enabled {
		return "", nil
	}
	if byteRange.Offset < 0 || byteRange.Length < 0 {
		return "", fmt.Errorf("invalid byte range: %w", ErrPermanent)
	}
	if byteRange.Length == 0 {
		return "", nil
	}
	if byteRange.Offset > math.MaxInt64-byteRange.Length+1 {
		return "", fmt.Errorf("byte range overflows int64: %w", ErrPermanent)
	}
	end := byteRange.Offset + byteRange.Length - 1
	return fmt.Sprintf("bytes=%d-%d", byteRange.Offset, end), nil
}

func classifyS3Error(op string, err error) error {
	if err == nil {
		return nil
	}

	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return wrapS3Error(op, classForS3Code(apiErr.ErrorCode(), apiErr.ErrorFault()), err)
	}

	var responseErr *smithyhttp.ResponseError
	if errors.As(err, &responseErr) {
		return wrapS3Error(op, classForS3Status(responseErr.HTTPStatusCode()), err)
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return wrapS3Error(op, ErrTransient, err)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return wrapS3Error(op, ErrTransient, err)
	}
	return wrapS3Error(op, ErrPermanent, err)
}

func classForS3Code(code string, fault smithy.ErrorFault) error {
	switch code {
	case "SlowDown", "ServiceUnavailable":
		return ErrThrottled
	case "InternalError", "RequestTimeout", "RequestTimeoutException":
		return ErrTransient
	case "AccessDenied", "ExpiredToken", "InvalidAccessKeyId", "InvalidToken", "SignatureDoesNotMatch":
		return ErrAuth
	case "NoSuchKey", "NoSuchBucket", "NotFound":
		return ErrNotFound
	case "ConditionalRequestConflict", "OperationAborted":
		return ErrConflict
	case "BadDigest", "ChecksumMismatch", "InvalidDigest":
		return ErrCorrupt
	case "BucketNotFound", "InvalidBucketName":
		return ErrPermanent
	default:
		return classForUnknownS3Code(fault)
	}
}

// classForUnknownS3Code classifies an unrecognized S3 error code by its fault.
// It must not blindly default to permanent (the restore path maps permanent to
// data-loss with no retry) nor blindly to transient (a genuine client/config
// error would burn retries and mask a permanent failure): server faults are
// transient, client faults permanent, unknown fault transient (safer for
// restore; the retry budget bounds it).
func classForUnknownS3Code(fault smithy.ErrorFault) error {
	if fault == smithy.FaultClient {
		return ErrPermanent
	}
	return ErrTransient
}

func classForS3Status(status int) error {
	if status <= 0 {
		return ErrTransient
	}
	switch status {
	case http.StatusTooManyRequests, http.StatusServiceUnavailable:
		return ErrThrottled
	case http.StatusRequestTimeout, http.StatusInternalServerError, http.StatusBadGateway, http.StatusGatewayTimeout:
		return ErrTransient
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrAuth
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusConflict, http.StatusPreconditionFailed:
		return ErrConflict
	default:
		// Unmapped 5xx is a server-side/transient condition; only unmapped 4xx
		// client errors are permanent. Avoids reporting an intact Block's restore
		// as data loss on an unrecognized transient status.
		if status >= http.StatusInternalServerError {
			return ErrTransient
		}
		return ErrPermanent
	}
}

func wrapS3Error(op string, class, err error) error {
	return fmt.Errorf("%s: %w: %w", op, class, err)
}

type s3PaginatedIterator struct {
	ctx       context.Context
	paginator *awss3.ListObjectsV2Paginator
	page      []types.Object
	pageIdx   int
	done      bool
}

func (it *s3PaginatedIterator) Next() (ObjectInfo, error) {
	for {
		if it.pageIdx < len(it.page) {
			obj := it.page[it.pageIdx]
			it.pageIdx++
			return objectInfoFromS3(obj)
		}
		if it.done || !it.paginator.HasMorePages() {
			return ObjectInfo{}, io.EOF
		}
		out, err := it.paginator.NextPage(it.ctx)
		if err != nil {
			it.done = true
			return ObjectInfo{}, classifyS3Error("list objects", err)
		}
		it.page = out.Contents
		it.pageIdx = 0
	}
}
