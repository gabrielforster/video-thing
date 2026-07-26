package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func testS3Client(t *testing.T) *s3.Client {
	t.Helper()
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}
	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String("http://localhost:4566")
		o.UsePathStyle = true
	})
}

func TestUploadURLSignsKeyAndExpires(t *testing.T) {
	p := NewPresigner(testS3Client(t), "video-thing-dev-raw-uploads", 15*time.Minute)

	url, expiresAt, err := p.UploadURL(context.Background(), "raw/3fa85f64-5717-4562-b3fc-2c963f66afa6")
	if err != nil {
		t.Fatalf("UploadURL: %v", err)
	}
	if !strings.Contains(url, "raw/3fa85f64-5717-4562-b3fc-2c963f66afa6") {
		t.Fatalf("url %q does not contain the object key", url)
	}
	if !strings.Contains(url, "X-Amz-Signature=") {
		t.Fatalf("url %q is not signed", url)
	}
	if !strings.Contains(url, "video-thing-dev-raw-uploads") {
		t.Fatalf("url %q does not contain the bucket", url)
	}
	if d := time.Until(expiresAt); d < 14*time.Minute || d > 15*time.Minute {
		t.Fatalf("expiresAt is %v out, want ~15m", d)
	}
}
