package backend

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // S3 Content-MD5 requires MD5 for transport integrity checks.
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

func TestS3BackendPutObjectSetsContentMD5(t *testing.T) {
	ctx := context.Background()
	payload := []byte("hello s3")
	sum := md5.Sum(payload) //nolint:gosec // S3 Content-MD5 requires MD5 for transport integrity checks.
	wantETag := hex.EncodeToString(sum[:])
	client := &mockS3Client{}
	client.putObject = func(_ context.Context, input *awss3.PutObjectInput, _ ...func(*awss3.Options)) (*awss3.PutObjectOutput, error) {
		assertS3PutInput(t, input, payload, base64.StdEncoding.EncodeToString(sum[:]))
		return &awss3.PutObjectOutput{ETag: aws.String(`"` + wantETag + `"`)}, nil
	}

	store := NewS3(client, "bucket-a")
	put, err := store.PutObject(ctx, "cell/shard/block.blk", bytes.NewReader(payload), int64(len(payload)), PutOpts{})
	if err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	if put.Size != int64(len(payload)) {
		t.Fatalf("Size = %d, want %d", put.Size, len(payload))
	}
	if put.ETag != wantETag {
		t.Fatalf("ETag = %q, want %q", put.ETag, wantETag)
	}
}

func assertS3PutInput(t *testing.T, input *awss3.PutObjectInput, payload []byte, contentMD5 string) {
	t.Helper()

	if got := aws.ToString(input.Bucket); got != "bucket-a" {
		t.Fatalf("Bucket = %q, want bucket-a", got)
	}
	if got := aws.ToString(input.Key); got != "cell/shard/block.blk" {
		t.Fatalf("Key = %q, want cell/shard/block.blk", got)
	}
	if got := aws.ToString(input.ContentMD5); got != contentMD5 {
		t.Fatalf("ContentMD5 = %q, want base64 md5", got)
	}
	if got := aws.ToInt64(input.ContentLength); got != int64(len(payload)) {
		t.Fatalf("ContentLength = %d, want %d", got, len(payload))
	}
	if got := aws.ToString(input.ContentType); got != DefaultContentType {
		t.Fatalf("ContentType = %q, want %q", got, DefaultContentType)
	}
	body, err := io.ReadAll(input.Body)
	if err != nil {
		t.Fatalf("Read body: %v", err)
	}
	if !bytes.Equal(body, payload) {
		t.Fatalf("body = %q, want %q", body, payload)
	}
}

func TestS3BackendPutObjectRejectsETagMismatch(t *testing.T) {
	client := &mockS3Client{
		putObject: func(context.Context, *awss3.PutObjectInput, ...func(*awss3.Options)) (*awss3.PutObjectOutput, error) {
			return &awss3.PutObjectOutput{ETag: aws.String(`"00000000000000000000000000000000"`)}, nil
		},
	}
	store := NewS3(client, "bucket-a")

	_, err := store.PutObject(context.Background(), "cell/shard/block.blk", bytes.NewReader([]byte("payload")), 7, PutOpts{})
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("PutObject error = %v, want ErrCorrupt", err)
	}
}

func TestS3BackendPutObjectRejectsInvalidBodies(t *testing.T) {
	store := NewS3(&mockS3Client{}, "bucket-a")

	tests := []struct {
		name string
		body []byte
		size int64
		want error
	}{
		{name: "negative size", body: []byte("payload"), size: -1, want: ErrPermanent},
		{name: "size mismatch", body: []byte("payload"), size: 8, want: ErrCorrupt},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := store.PutObject(context.Background(), "cell/shard/block.blk", bytes.NewReader(tt.body), tt.size, PutOpts{})
			if !errors.Is(err, tt.want) {
				t.Fatalf("PutObject error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestS3BackendHeadObjectReturnsVerifiedMeta(t *testing.T) {
	client := &mockS3Client{}
	client.headObject = func(_ context.Context, input *awss3.HeadObjectInput, _ ...func(*awss3.Options)) (*awss3.HeadObjectOutput, error) {
		if got := aws.ToString(input.Bucket); got != "bucket-a" {
			t.Fatalf("Bucket = %q, want bucket-a", got)
		}
		return &awss3.HeadObjectOutput{
			ContentLength: aws.Int64(16),
			ContentType:   aws.String("application/block"),
			ETag:          aws.String(`"0123456789abcdef0123456789abcdef"`),
		}, nil
	}
	store := NewS3(client, "bucket-a")

	meta, err := store.HeadObject(context.Background(), "cell/shard/block.blk")
	if err != nil {
		t.Fatalf("HeadObject: %v", err)
	}
	if meta.Size != 16 || meta.ETag != "0123456789abcdef0123456789abcdef" || meta.ContentType != "application/block" {
		t.Fatalf("meta = %+v, want size/content-type/etag from S3", meta)
	}
}

func TestS3BackendHeadObjectRejectsInvalidMetadata(t *testing.T) {
	tests := []struct {
		name string
		out  *awss3.HeadObjectOutput
	}{
		{name: "nil output"},
		{name: "missing content length", out: &awss3.HeadObjectOutput{ETag: aws.String(`"0123456789abcdef0123456789abcdef"`)}},
		{name: "negative content length", out: &awss3.HeadObjectOutput{
			ContentLength: aws.Int64(-1),
			ETag:          aws.String(`"0123456789abcdef0123456789abcdef"`),
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &mockS3Client{}
			client.headObject = func(context.Context, *awss3.HeadObjectInput, ...func(*awss3.Options)) (*awss3.HeadObjectOutput, error) {
				return tt.out, nil
			}
			store := NewS3(client, "bucket-a")

			_, err := store.HeadObject(context.Background(), "cell/shard/block.blk")
			if !errors.Is(err, ErrCorrupt) {
				t.Fatalf("HeadObject error = %v, want ErrCorrupt", err)
			}
		})
	}
}

func TestS3BackendHeadObjectAcceptsNonMD5ETag(t *testing.T) {
	client := &mockS3Client{}
	client.headObject = func(context.Context, *awss3.HeadObjectInput, ...func(*awss3.Options)) (*awss3.HeadObjectOutput, error) {
		return &awss3.HeadObjectOutput{
			ContentLength: aws.Int64(16),
			ETag:          aws.String(`"not-md5-etag"`),
		}, nil
	}
	store := NewS3(client, "bucket-a")

	meta, err := store.HeadObject(context.Background(), "cell/shard/block.blk")
	if err != nil {
		t.Fatalf("HeadObject: %v", err)
	}
	if meta.ETag != "not-md5-etag" {
		t.Fatalf("ETag = %q, want %q", meta.ETag, "not-md5-etag")
	}
}

func TestS3BackendGetObjectUsesRangeHeader(t *testing.T) {
	client := &mockS3Client{}
	client.headObject = func(context.Context, *awss3.HeadObjectInput, ...func(*awss3.Options)) (*awss3.HeadObjectOutput, error) {
		return &awss3.HeadObjectOutput{
			ContentLength: aws.Int64(16),
			ContentType:   aws.String(DefaultContentType),
			ETag:          aws.String(`"0123456789abcdef0123456789abcdef"`),
		}, nil
	}
	client.getObject = func(_ context.Context, input *awss3.GetObjectInput, _ ...func(*awss3.Options)) (*awss3.GetObjectOutput, error) {
		if got := aws.ToString(input.Range); got != "bytes=4-9" {
			t.Fatalf("Range = %q, want bytes=4-9", got)
		}
		return &awss3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader([]byte("456789")))}, nil
	}
	store := NewS3(client, "bucket-a")

	rc, meta, err := store.GetObject(context.Background(), "cell/shard/block.blk", GetOpts{
		Range: ByteRange{Enabled: true, Offset: 4, Length: 6},
	})
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	defer func() { _ = rc.Close() }()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != "456789" {
		t.Fatalf("body = %q, want 456789", got)
	}
	if meta.Size != 16 {
		t.Fatalf("meta size = %d, want full object size 16", meta.Size)
	}
}

func TestS3BackendGetObjectZeroLengthRangeSkipsGet(t *testing.T) {
	client := &mockS3Client{}
	client.headObject = validS3Head
	client.getObject = func(context.Context, *awss3.GetObjectInput, ...func(*awss3.Options)) (*awss3.GetObjectOutput, error) {
		t.Fatal("GetObject should not be called for a zero-length range")
		return nil, errors.New("unexpected GetObject call")
	}
	store := NewS3(client, "bucket-a")

	rc, meta, err := store.GetObject(context.Background(), "cell/shard/block.blk", GetOpts{
		Range: ByteRange{Enabled: true, Offset: 4},
	})
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	defer func() { _ = rc.Close() }()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("body length = %d, want 0", len(got))
	}
	if meta.Size != 16 {
		t.Fatalf("meta size = %d, want 16", meta.Size)
	}
}

func TestS3BackendGetObjectRejectsInvalidRanges(t *testing.T) {
	tests := []struct {
		name      string
		byteRange ByteRange
	}{
		{name: "negative offset", byteRange: ByteRange{Enabled: true, Offset: -1, Length: 1}},
		{name: "negative length", byteRange: ByteRange{Enabled: true, Offset: 0, Length: -1}},
		{name: "overflow", byteRange: ByteRange{Enabled: true, Offset: math.MaxInt64, Length: 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &mockS3Client{}
			client.headObject = validS3Head
			client.getObject = func(context.Context, *awss3.GetObjectInput, ...func(*awss3.Options)) (*awss3.GetObjectOutput, error) {
				t.Fatal("GetObject should not be called for an invalid range")
				return nil, errors.New("unexpected GetObject call")
			}
			store := NewS3(client, "bucket-a")

			_, _, err := store.GetObject(context.Background(), "cell/shard/block.blk", GetOpts{Range: tt.byteRange})
			if !errors.Is(err, ErrPermanent) {
				t.Fatalf("GetObject error = %v, want ErrPermanent", err)
			}
		})
	}
}

func TestS3BackendListObjectsPaginates(t *testing.T) {
	client := &mockS3Client{}
	var tokens []string
	client.listObjectsV2 = func(_ context.Context, input *awss3.ListObjectsV2Input, _ ...func(*awss3.Options)) (*awss3.ListObjectsV2Output, error) {
		tokens = append(tokens, aws.ToString(input.ContinuationToken))
		if aws.ToString(input.Prefix) != "cell/shard/" {
			t.Fatalf("Prefix = %q, want cell/shard/", aws.ToString(input.Prefix))
		}
		return listPage(input.ContinuationToken), nil
	}
	store := NewS3(client, "bucket-a")

	iter, err := store.ListObjects(context.Background(), "cell/shard/", ListOpts{})
	if err != nil {
		t.Fatalf("ListObjects: %v", err)
	}
	first, err := iter.Next()
	if err != nil {
		t.Fatalf("first Next: %v", err)
	}
	second, err := iter.Next()
	if err != nil {
		t.Fatalf("second Next: %v", err)
	}
	if _, err := iter.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("final Next error = %v, want EOF", err)
	}
	assertListedS3Objects(t, first, second, tokens)
}

func listPage(continuationToken *string) *awss3.ListObjectsV2Output {
	if continuationToken == nil {
		return &awss3.ListObjectsV2Output{
			IsTruncated:           aws.Bool(true),
			NextContinuationToken: aws.String("next"),
			Contents: []types.Object{{
				Key:  aws.String("cell/shard/a.blk"),
				ETag: aws.String(`"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`),
				Size: aws.Int64(10),
			}},
		}
	}
	return &awss3.ListObjectsV2Output{
		IsTruncated: aws.Bool(false),
		Contents: []types.Object{{
			Key:  aws.String("cell/shard/b.blk"),
			ETag: aws.String(`"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"`),
			Size: aws.Int64(20),
		}},
	}
}

func assertListedS3Objects(t *testing.T, first, second ObjectInfo, tokens []string) {
	t.Helper()

	if first.Key != "cell/shard/a.blk" || second.Key != "cell/shard/b.blk" {
		t.Fatalf("listed keys = %q, %q", first.Key, second.Key)
	}
	if len(tokens) != 2 || tokens[0] != "" || tokens[1] != "next" {
		t.Fatalf("continuation tokens = %v, want [\"\" \"next\"]", tokens)
	}
}

func TestS3BackendListObjectsRejectsInvalidResponses(t *testing.T) {
	tests := []struct {
		name   string
		object types.Object
	}{
		{name: "missing key", object: types.Object{
			ETag: aws.String(`"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`),
			Size: aws.Int64(10),
		}},
		{name: "missing size", object: types.Object{
			Key:  aws.String("cell/shard/a.blk"),
			ETag: aws.String(`"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`),
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &mockS3Client{}
			client.listObjectsV2 = func(context.Context, *awss3.ListObjectsV2Input, ...func(*awss3.Options)) (*awss3.ListObjectsV2Output, error) {
				return &awss3.ListObjectsV2Output{Contents: []types.Object{tt.object}}, nil
			}
			store := NewS3(client, "bucket-a")

			iter, err := store.ListObjects(context.Background(), "cell/shard/", ListOpts{})
			if err != nil {
				t.Fatalf("ListObjects should not fail for lazy iterator: %v", err)
			}
			_, err = iter.Next()
			if !errors.Is(err, ErrCorrupt) {
				t.Fatalf("Next error = %v, want ErrCorrupt", err)
			}
		})
	}
}

func TestS3BackendListObjectsAcceptsNonMD5ETag(t *testing.T) {
	client := &mockS3Client{}
	client.listObjectsV2 = func(context.Context, *awss3.ListObjectsV2Input, ...func(*awss3.Options)) (*awss3.ListObjectsV2Output, error) {
		return &awss3.ListObjectsV2Output{Contents: []types.Object{{
			Key:  aws.String("cell/shard/a.blk"),
			ETag: aws.String(`"not-md5"`),
			Size: aws.Int64(10),
		}}}, nil
	}
	store := NewS3(client, "bucket-a")

	iter, err := store.ListObjects(context.Background(), "cell/shard/", ListOpts{})
	if err != nil {
		t.Fatalf("ListObjects: %v", err)
	}
	obj, err := iter.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if obj.ETag != "not-md5" {
		t.Fatalf("ETag = %q, want %q", obj.ETag, "not-md5")
	}
}

func TestS3BackendRejectsInvalidListRequests(t *testing.T) {
	tests := []struct {
		name   string
		bucket string
		prefix string
	}{
		{name: "empty bucket", prefix: "cell/shard/"},
		{name: "invalid prefix", bucket: "bucket-a", prefix: "../escape"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewS3(&mockS3Client{}, tt.bucket)

			_, err := store.ListObjects(context.Background(), tt.prefix, ListOpts{})
			if !errors.Is(err, ErrPermanent) {
				t.Fatalf("ListObjects error = %v, want ErrPermanent", err)
			}
		})
	}
}

func TestS3BackendDeleteObject(t *testing.T) {
	client := &mockS3Client{}
	client.deleteObject = func(_ context.Context, input *awss3.DeleteObjectInput, _ ...func(*awss3.Options)) (*awss3.DeleteObjectOutput, error) {
		if got := aws.ToString(input.Key); got != "cell/shard/block.blk" {
			t.Fatalf("Key = %q, want cell/shard/block.blk", got)
		}
		return &awss3.DeleteObjectOutput{}, nil
	}
	store := NewS3(client, "bucket-a")

	if err := store.DeleteObject(context.Background(), "cell/shard/block.blk"); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}
}

func validS3Head(context.Context, *awss3.HeadObjectInput, ...func(*awss3.Options)) (*awss3.HeadObjectOutput, error) {
	return &awss3.HeadObjectOutput{
		ContentLength: aws.Int64(16),
		ContentType:   aws.String(DefaultContentType),
		ETag:          aws.String(`"0123456789abcdef0123456789abcdef"`),
	}, nil
}

func TestClassifyS3Error(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "slow down", err: apiErr("SlowDown"), want: ErrThrottled},
		{name: "service unavailable", err: responseErr(http.StatusServiceUnavailable), want: ErrThrottled},
		{name: "too many requests", err: responseErr(http.StatusTooManyRequests), want: ErrThrottled},
		{name: "internal error", err: apiErr("InternalError"), want: ErrTransient},
		{name: "internal error status", err: responseErr(http.StatusInternalServerError), want: ErrTransient},
		{name: "bad gateway", err: responseErr(http.StatusBadGateway), want: ErrTransient},
		{name: "gateway timeout", err: responseErr(http.StatusGatewayTimeout), want: ErrTransient},
		{name: "request timeout", err: responseErr(http.StatusRequestTimeout), want: ErrTransient},
		{name: "status zero", err: responseErr(0), want: ErrTransient},
		{name: "timeout", err: timeoutError{}, want: ErrTransient},
		{name: "cancelled", err: context.Canceled, want: ErrTransient},
		{name: "expired token", err: apiErr("ExpiredToken"), want: ErrAuth},
		{name: "invalid access key", err: apiErr("InvalidAccessKeyId"), want: ErrAuth},
		{name: "unauthorized", err: responseErr(http.StatusUnauthorized), want: ErrAuth},
		{name: "access denied", err: responseErr(http.StatusForbidden), want: ErrAuth},
		{name: "no such key", err: apiErr("NoSuchKey"), want: ErrNotFound},
		{name: "no such bucket", err: apiErr("NoSuchBucket"), want: ErrNotFound},
		{name: "not found status", err: responseErr(http.StatusNotFound), want: ErrNotFound},
		{name: "conditional conflict", err: apiErr("ConditionalRequestConflict"), want: ErrConflict},
		{name: "conflict", err: responseErr(http.StatusConflict), want: ErrConflict},
		{name: "precondition failed", err: responseErr(http.StatusPreconditionFailed), want: ErrConflict},
		{name: "bad digest", err: apiErr("BadDigest"), want: ErrCorrupt},
		{name: "checksum mismatch", err: apiErr("ChecksumMismatch"), want: ErrCorrupt},
		{name: "bucket not found", err: apiErr("BucketNotFound"), want: ErrPermanent},
		{name: "invalid bucket", err: apiErr("InvalidBucketName"), want: ErrPermanent},
		// An unrecognized provider code with no fault classification defaults to
		// transient (safer for restore than permanent → data-loss).
		{name: "unknown no fault", err: apiErr("Weird"), want: ErrTransient},
		// An unrecognized server-fault code is transient; an unrecognized
		// client-fault code stays permanent (a genuine client/config error must
		// not burn retries or be masked as transient).
		{name: "unknown server fault", err: apiErrFault("WeirdServer", smithy.FaultServer), want: ErrTransient},
		{name: "unknown client fault", err: apiErrFault("WeirdClient", smithy.FaultClient), want: ErrPermanent},
		// An unrecognized code with no fault but a 4xx HTTP status must stay
		// permanent (status wins over the transient fault default); a 5xx status
		// is transient.
		{name: "unknown code 4xx status", err: apiErrResponse("Weird", smithy.FaultUnknown, http.StatusBadRequest), want: ErrPermanent},
		{name: "unknown code 5xx status", err: apiErrResponse("Weird", smithy.FaultUnknown, http.StatusInternalServerError), want: ErrTransient},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := classifyS3Error("test op", tt.err)
			if !errors.Is(err, tt.want) {
				t.Fatalf("classifyS3Error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestNewS3FromConfigUsesAWSDefaultChain(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")

	store, err := NewS3FromConfig(context.Background(), S3Config{
		Bucket:    "bucket-a",
		Region:    "us-east-1",
		Endpoint:  "http://localhost:4566",
		PathStyle: true,
	})
	if err != nil {
		t.Fatalf("NewS3FromConfig: %v", err)
	}
	if store == nil {
		t.Fatal("NewS3FromConfig returned nil store")
	}
}

func TestParseS3ConfigFromEnv(t *testing.T) {
	t.Setenv("SCRAP_S3_BUCKET", "bucket-a")
	t.Setenv("SCRAP_S3_REGION", "us-east-1")
	t.Setenv("SCRAP_S3_ENDPOINT", "http://localhost:4566")
	t.Setenv("SCRAP_S3_PATH_STYLE", "true")

	cfg, err := ParseS3ConfigFromEnv()
	if err != nil {
		t.Fatalf("ParseS3ConfigFromEnv: %v", err)
	}
	if cfg.Bucket != "bucket-a" || cfg.Region != "us-east-1" || cfg.Endpoint != "http://localhost:4566" || !cfg.PathStyle {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestParseS3ConfigFromEnvRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
	}{
		{name: "missing bucket", env: map[string]string{"SCRAP_S3_REGION": "us-east-1"}},
		{name: "missing region", env: map[string]string{"SCRAP_S3_BUCKET": "bucket-a"}},
		{name: "invalid path style", env: map[string]string{
			"SCRAP_S3_BUCKET":     "bucket-a",
			"SCRAP_S3_REGION":     "us-east-1",
			"SCRAP_S3_PATH_STYLE": "sometimes",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for key, value := range tt.env {
				t.Setenv(key, value)
			}

			_, err := ParseS3ConfigFromEnv()
			if !errors.Is(err, ErrPermanent) {
				t.Fatalf("ParseS3ConfigFromEnv error = %v, want ErrPermanent", err)
			}
		})
	}
}

type mockS3Client struct {
	putObject     func(context.Context, *awss3.PutObjectInput, ...func(*awss3.Options)) (*awss3.PutObjectOutput, error)
	headObject    func(context.Context, *awss3.HeadObjectInput, ...func(*awss3.Options)) (*awss3.HeadObjectOutput, error)
	getObject     func(context.Context, *awss3.GetObjectInput, ...func(*awss3.Options)) (*awss3.GetObjectOutput, error)
	deleteObject  func(context.Context, *awss3.DeleteObjectInput, ...func(*awss3.Options)) (*awss3.DeleteObjectOutput, error)
	listObjectsV2 func(context.Context, *awss3.ListObjectsV2Input, ...func(*awss3.Options)) (*awss3.ListObjectsV2Output, error)
}

func (m *mockS3Client) PutObject(ctx context.Context, input *awss3.PutObjectInput, opts ...func(*awss3.Options)) (*awss3.PutObjectOutput, error) {
	return m.putObject(ctx, input, opts...)
}

func (m *mockS3Client) HeadObject(ctx context.Context, input *awss3.HeadObjectInput, opts ...func(*awss3.Options)) (*awss3.HeadObjectOutput, error) {
	return m.headObject(ctx, input, opts...)
}

func (m *mockS3Client) GetObject(ctx context.Context, input *awss3.GetObjectInput, opts ...func(*awss3.Options)) (*awss3.GetObjectOutput, error) {
	return m.getObject(ctx, input, opts...)
}

func (m *mockS3Client) DeleteObject(ctx context.Context, input *awss3.DeleteObjectInput, opts ...func(*awss3.Options)) (*awss3.DeleteObjectOutput, error) {
	return m.deleteObject(ctx, input, opts...)
}

func (m *mockS3Client) ListObjectsV2(ctx context.Context, input *awss3.ListObjectsV2Input, opts ...func(*awss3.Options)) (*awss3.ListObjectsV2Output, error) {
	return m.listObjectsV2(ctx, input, opts...)
}

func apiErr(code string) error {
	return &smithy.GenericAPIError{Code: code, Message: code}
}

func apiErrFault(code string, fault smithy.ErrorFault) error {
	return &smithy.GenericAPIError{Code: code, Message: code, Fault: fault}
}

// apiErrResponse builds an error that unwraps as both smithy.APIError and
// smithyhttp.ResponseError, modeling an unmodeled provider code carried on an
// HTTP response with a known status.
func apiErrResponse(code string, fault smithy.ErrorFault, status int) error {
	return &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{Response: &http.Response{StatusCode: status}},
		Err:      &smithy.GenericAPIError{Code: code, Message: code, Fault: fault},
	}
}

func responseErr(status int) error {
	return &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{Response: &http.Response{StatusCode: status}},
		Err:      errors.New(http.StatusText(status)),
	}
}

type timeoutError struct{}

func (timeoutError) Error() string {
	return "timeout"
}

func (timeoutError) Timeout() bool {
	return true
}

func (timeoutError) Temporary() bool {
	return true
}

var _ net.Error = timeoutError{}

var _ S3Client = (*mockS3Client)(nil)
