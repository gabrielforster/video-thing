package db_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gabrielforster/video-thing/packages/database/db"
)

func testQueries(t *testing.T) *db.Queries {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping database test")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return db.New(pool)
}

func TestCreateThenGetVideo(t *testing.T) {
	ctx := context.Background()
	q := testQueries(t)
	id := uuid.New()

	created, err := q.CreateVideo(ctx, db.CreateVideoParams{
		ID:           id,
		Title:        "test clip",
		SourceBucket: "video-thing-dev-raw-uploads",
		SourceKey:    "raw/" + id.String(),
	})
	if err != nil {
		t.Fatalf("CreateVideo: %v", err)
	}
	if created.Status != db.VideoStatusUploading {
		t.Fatalf("status = %q, want uploading", created.Status)
	}

	got, err := q.GetVideo(ctx, id)
	if err != nil {
		t.Fatalf("GetVideo: %v", err)
	}
	if got.Title != "test clip" {
		t.Fatalf("title = %q, want %q", got.Title, "test clip")
	}
}

func TestMarkReadySetsMetadata(t *testing.T) {
	ctx := context.Background()
	q := testQueries(t)
	id := uuid.New()

	if _, err := q.CreateVideo(ctx, db.CreateVideoParams{
		ID:           id,
		Title:        "ready clip",
		SourceBucket: "video-thing-dev-raw-uploads",
		SourceKey:    "raw/" + id.String(),
	}); err != nil {
		t.Fatalf("CreateVideo: %v", err)
	}

	duration, width, height, size := 12.5, int32(1280), int32(720), int64(4242)
	playlist, thumb := "processed/"+id.String()+"/master.m3u8", "processed/"+id.String()+"/thumbnails/cover.jpg"

	got, err := q.MarkReady(ctx, db.MarkReadyParams{
		ID:             id,
		Duration:       &duration,
		Width:          &width,
		Height:         &height,
		SizeBytes:      &size,
		MasterPlaylist: &playlist,
		Thumbnail:      &thumb,
	})
	if err != nil {
		t.Fatalf("MarkReady: %v", err)
	}
	if got.Status != db.VideoStatusReady {
		t.Fatalf("status = %q, want ready", got.Status)
	}
	if got.Duration == nil || *got.Duration != duration {
		t.Fatalf("duration = %v, want %v", got.Duration, duration)
	}
}
