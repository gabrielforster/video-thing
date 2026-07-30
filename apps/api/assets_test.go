package main

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"

	"github.com/gabrielforster/video-thing/packages/database/db"
)

type fakeS3 struct {
	pages       [][]string
	pageIndex   int
	listErr     error
	deleteErr   error
	deleteCalls [][]string
}

func (f *fakeS3) ListObjectsV2(_ context.Context, _ *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.pageIndex >= len(f.pages) {
		return &s3.ListObjectsV2Output{}, nil
	}
	keys := f.pages[f.pageIndex]
	f.pageIndex++
	contents := make([]types.Object, len(keys))
	for i, k := range keys {
		contents[i] = types.Object{Key: aws.String(k)}
	}
	out := &s3.ListObjectsV2Output{Contents: contents}
	if f.pageIndex < len(f.pages) {
		out.IsTruncated = aws.Bool(true)
		out.NextContinuationToken = aws.String("token-" + strconv.Itoa(f.pageIndex))
	}
	return out, nil
}

func (f *fakeS3) DeleteObjects(_ context.Context, in *s3.DeleteObjectsInput, _ ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	keys := make([]string, len(in.Delete.Objects))
	for i, obj := range in.Delete.Objects {
		keys[i] = aws.ToString(obj.Key)
	}
	f.deleteCalls = append(f.deleteCalls, keys)
	return &s3.DeleteObjectsOutput{}, nil
}

func TestDeleteVideoAssetsDeletesRawKeyThenProcessedPrefix(t *testing.T) {
	id := uuid.New()
	video := db.Video{ID: id, SourceBucket: "raw-bucket", SourceKey: "raw/" + id.String()}
	processedKeys := []string{"processed/" + id.String() + "/master.m3u8", "processed/" + id.String() + "/720/playlist.m3u8"}

	fake := &fakeS3{pages: [][]string{processedKeys}}
	cleaner := NewS3AssetCleaner(fake, "processed-bucket")

	if err := cleaner.deleteVideoAssets(context.Background(), video); err != nil {
		t.Fatalf("deleteVideoAssets: %v", err)
	}

	if len(fake.deleteCalls) != 2 {
		t.Fatalf("len(deleteCalls) = %d, want 2", len(fake.deleteCalls))
	}
	if len(fake.deleteCalls[0]) != 1 || fake.deleteCalls[0][0] != video.SourceKey {
		t.Fatalf("first DeleteObjects call = %v, want [%s]", fake.deleteCalls[0], video.SourceKey)
	}
	if len(fake.deleteCalls[1]) != len(processedKeys) {
		t.Fatalf("second DeleteObjects call = %v, want %v", fake.deleteCalls[1], processedKeys)
	}
}

func TestDeleteVideoAssetsBatchesDeleteObjectsAt1000Keys(t *testing.T) {
	id := uuid.New()
	video := db.Video{ID: id, SourceBucket: "raw-bucket", SourceKey: "raw/" + id.String()}

	var page1, page2 []string
	for i := 0; i < 1000; i++ {
		page1 = append(page1, "processed/"+id.String()+"/"+strconv.Itoa(i))
	}
	for i := 1000; i < 1500; i++ {
		page2 = append(page2, "processed/"+id.String()+"/"+strconv.Itoa(i))
	}

	fake := &fakeS3{pages: [][]string{page1, page2}}
	cleaner := NewS3AssetCleaner(fake, "processed-bucket")

	if err := cleaner.deleteVideoAssets(context.Background(), video); err != nil {
		t.Fatalf("deleteVideoAssets: %v", err)
	}

	if len(fake.deleteCalls) != 3 {
		t.Fatalf("len(deleteCalls) = %d, want 3 (1 raw + 2 processed batches)", len(fake.deleteCalls))
	}
	if len(fake.deleteCalls[1]) != 1000 {
		t.Fatalf("first processed batch = %d keys, want 1000", len(fake.deleteCalls[1]))
	}
	if len(fake.deleteCalls[2]) != 500 {
		t.Fatalf("second processed batch = %d keys, want 500", len(fake.deleteCalls[2]))
	}
}

func TestDeleteVideoAssetsPropagatesListError(t *testing.T) {
	id := uuid.New()
	video := db.Video{ID: id, SourceBucket: "raw-bucket", SourceKey: "raw/" + id.String()}
	fake := &fakeS3{listErr: errors.New("list boom")}
	cleaner := NewS3AssetCleaner(fake, "processed-bucket")

	if err := cleaner.deleteVideoAssets(context.Background(), video); err == nil {
		t.Fatal("expected an error when ListObjectsV2 fails")
	}
}

func TestDeleteVideoAssetsPropagatesDeleteError(t *testing.T) {
	id := uuid.New()
	video := db.Video{ID: id, SourceBucket: "raw-bucket", SourceKey: "raw/" + id.String()}
	fake := &fakeS3{deleteErr: errors.New("delete boom")}
	cleaner := NewS3AssetCleaner(fake, "processed-bucket")

	if err := cleaner.deleteVideoAssets(context.Background(), video); err == nil {
		t.Fatal("expected an error when DeleteObjects fails")
	}
}
