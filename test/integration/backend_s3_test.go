//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/test/integration/testinfra"
	scraplocalstack "github.com/petabytecl/scrap/test/integration/testinfra/localstack"
)

//nolint:gocognit,cyclop // LocalStack integration exercises the full required S3 round trip in one scenario.
//goland:noinspection ALL
func TestIntegrationS3BackendLocalStackRoundTrip(t *testing.T) {
	ctx := integrationTestContext(t)
	stack, err := scraplocalstack.Run(ctx, scraplocalstack.DefaultImage)
	if stack != nil {
		testinfra.CleanupContainer(t, stack)
	}
	if err != nil {
		t.Fatalf("start LocalStack testcontainer: %v", err)
	}
	bucket := fmt.Sprintf("scrap-s3-test-%d", time.Now().UnixNano())

	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(scraplocalstack.DefaultRegion),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("load AWS config: %v", err)
	}
	client := awss3.NewFromConfig(awsCfg, func(o *awss3.Options) {
		endpoint, err := stack.HTTPHostAddress(ctx)
		if err != nil {
			t.Fatalf("resolve LocalStack endpoint: %v", err)
		}
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})
	if _, err := client.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = client.DeleteBucket(cleanupCtx, &awss3.DeleteBucketInput{Bucket: aws.String(bucket)})
	})

	s3Config, err := stack.S3Config(ctx, bucket)
	if err != nil {
		t.Fatalf("build S3 config: %v", err)
	}
	store, err := backend.NewS3FromConfig(ctx, s3Config)
	if err != nil {
		t.Fatalf("NewS3FromConfig: %v", err)
	}

	key := "cell/shard/block.blk"
	payload := []byte("0123456789abcdef")
	put, err := store.PutObject(ctx, key, bytes.NewReader(payload), int64(len(payload)), backend.PutOpts{})
	if err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	head, err := store.HeadObject(ctx, key)
	if err != nil {
		t.Fatalf("HeadObject: %v", err)
	}
	if head.Size != int64(len(payload)) || head.ETag != put.ETag {
		t.Fatalf("head = %+v, put = %+v", head, put)
	}

	full, _, err := store.GetObject(ctx, key, backend.GetOpts{})
	if err != nil {
		t.Fatalf("GetObject full: %v", err)
	}
	fullBody, err := io.ReadAll(full)
	if closeErr := full.Close(); closeErr != nil {
		t.Fatalf("close full body: %v", closeErr)
	}
	if err != nil {
		t.Fatalf("read full body: %v", err)
	}
	if !bytes.Equal(fullBody, payload) {
		t.Fatalf("full body = %q, want %q", fullBody, payload)
	}

	ranged, _, err := store.GetObject(ctx, key, backend.GetOpts{
		Range: backend.ByteRange{Enabled: true, Offset: 4, Length: 6},
	})
	if err != nil {
		t.Fatalf("GetObject range: %v", err)
	}
	rangeBody, err := io.ReadAll(ranged)
	if closeErr := ranged.Close(); closeErr != nil {
		t.Fatalf("close range body: %v", closeErr)
	}
	if err != nil {
		t.Fatalf("read range body: %v", err)
	}
	if string(rangeBody) != "456789" {
		t.Fatalf("range body = %q, want 456789", rangeBody)
	}

	iter, err := store.ListObjects(ctx, "cell/shard/", backend.ListOpts{})
	if err != nil {
		t.Fatalf("ListObjects: %v", err)
	}
	info, err := iter.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if info.Key != key {
		t.Fatalf("listed key = %q, want %q", info.Key, key)
	}
	if err := store.DeleteObject(ctx, key); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}
}
