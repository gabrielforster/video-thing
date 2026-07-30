package main

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/gabrielforster/video-thing/packages/database/db"
)

const maxDeleteBatch = 1000

type s3API interface {
	ListObjectsV2(context.Context, *s3.ListObjectsV2Input, ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
	DeleteObjects(context.Context, *s3.DeleteObjectsInput, ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error)
}

type S3AssetCleaner struct {
	s3              s3API
	processedBucket string
}

func NewS3AssetCleaner(client s3API, processedBucket string) *S3AssetCleaner {
	return &S3AssetCleaner{s3: client, processedBucket: processedBucket}
}

func (a *S3AssetCleaner) deleteVideoAssets(ctx context.Context, v db.Video) error {
	if err := a.deleteBatch(ctx, v.SourceBucket, []string{v.SourceKey}); err != nil {
		return fmt.Errorf("delete raw object: %w", err)
	}

	prefix := "processed/" + v.ID.String() + "/"
	keys, err := a.listKeys(ctx, a.processedBucket, prefix)
	if err != nil {
		return fmt.Errorf("list processed objects: %w", err)
	}
	if err := a.deleteBatch(ctx, a.processedBucket, keys); err != nil {
		return fmt.Errorf("delete processed objects: %w", err)
	}
	return nil
}

func (a *S3AssetCleaner) listKeys(ctx context.Context, bucket, prefix string) ([]string, error) {
	var keys []string
	paginator := s3.NewListObjectsV2Paginator(a.s3, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
		Prefix: aws.String(prefix),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, obj := range page.Contents {
			keys = append(keys, aws.ToString(obj.Key))
		}
	}
	return keys, nil
}

func (a *S3AssetCleaner) deleteBatch(ctx context.Context, bucket string, keys []string) error {
	for len(keys) > 0 {
		n := min(len(keys), maxDeleteBatch)
		batch := keys[:n]
		keys = keys[n:]

		ids := make([]types.ObjectIdentifier, len(batch))
		for i, k := range batch {
			ids[i] = types.ObjectIdentifier{Key: aws.String(k)}
		}
		if _, err := a.s3.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(bucket),
			Delete: &types.Delete{Objects: ids},
		}); err != nil {
			return err
		}
	}
	return nil
}
