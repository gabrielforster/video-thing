package main

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// UploadContentType is signed into the presigned URL, so the client's PUT
// must send this exact header value or S3 rejects the signature.
const UploadContentType = "application/octet-stream"

type Presigner struct {
	client *s3.PresignClient
	bucket string
	ttl    time.Duration
}

func NewPresigner(client *s3.Client, bucket string, ttl time.Duration) *Presigner {
	return &Presigner{client: s3.NewPresignClient(client), bucket: bucket, ttl: ttl}
}

func (p *Presigner) UploadURL(ctx context.Context, key string) (string, time.Time, error) {
	req, err := p.client.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(p.bucket),
		Key:         aws.String(key),
		ContentType: aws.String(UploadContentType),
	}, s3.WithPresignExpires(p.ttl))
	if err != nil {
		return "", time.Time{}, err
	}
	return req.URL, time.Now().UTC().Add(p.ttl), nil
}
