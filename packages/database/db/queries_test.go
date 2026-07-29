package db_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

func createTestVideo(t *testing.T, ctx context.Context, q *db.Queries, title string) db.Video {
	t.Helper()
	id := uuid.New()
	v, err := q.CreateVideo(ctx, db.CreateVideoParams{
		ID:           id,
		Title:        title,
		SourceBucket: "video-thing-dev-raw-uploads",
		SourceKey:    "raw/" + id.String(),
	})
	if err != nil {
		t.Fatalf("CreateVideo: %v", err)
	}
	return v
}

func TestListVideosOrdersByCreatedAtDescWithLimitAndOffset(t *testing.T) {
	ctx := context.Background()
	q := testQueries(t)

	// The table is shared across test runs and never truncated (see
	// TestCountVideosCountsAllRows below), so these assertions can't assume
	// an empty table -- they only rely on the 3 rows just created being the
	// newest in it.
	var ids []uuid.UUID
	for i := 0; i < 3; i++ {
		ids = append(ids, createTestVideo(t, ctx, q, "list test").ID)
		time.Sleep(10 * time.Millisecond)
	}

	head, err := q.ListVideos(ctx, db.ListVideosParams{Limit: 3, Offset: 0})
	if err != nil {
		t.Fatalf("ListVideos: %v", err)
	}
	if len(head) != 3 {
		t.Fatalf("len(head) = %d, want 3", len(head))
	}
	if head[0].ID != ids[2] || head[1].ID != ids[1] || head[2].ID != ids[0] {
		t.Fatalf("head = [%s, %s, %s], want newest-first [%s, %s, %s]",
			head[0].ID, head[1].ID, head[2].ID, ids[2], ids[1], ids[0])
	}

	rest, err := q.ListVideos(ctx, db.ListVideosParams{Limit: 10, Offset: 3})
	if err != nil {
		t.Fatalf("ListVideos (offset=3): %v", err)
	}
	for _, v := range rest {
		for _, id := range ids {
			if v.ID == id {
				t.Fatalf("offset=3 page still contains %s, one of the 3 newest rows", id)
			}
		}
	}
}

func TestCountVideosCountsAllRows(t *testing.T) {
	ctx := context.Background()
	q := testQueries(t)

	before, err := q.CountVideos(ctx)
	if err != nil {
		t.Fatalf("CountVideos (before): %v", err)
	}

	for i := 0; i < 3; i++ {
		createTestVideo(t, ctx, q, "count test")
	}

	after, err := q.CountVideos(ctx)
	if err != nil {
		t.Fatalf("CountVideos (after): %v", err)
	}
	if after != before+3 {
		t.Fatalf("CountVideos = %d, want %d (before %d + 3 created)", after, before+3, before)
	}
}

func TestDeleteVideoRemovesRowAndReturnsIt(t *testing.T) {
	ctx := context.Background()
	q := testQueries(t)
	created := createTestVideo(t, ctx, q, "delete test")

	deleted, err := q.DeleteVideo(ctx, created.ID)
	if err != nil {
		t.Fatalf("DeleteVideo: %v", err)
	}
	if deleted.SourceKey != created.SourceKey || deleted.SourceBucket != created.SourceBucket {
		t.Fatalf("DeleteVideo returned %+v, want source_key/source_bucket matching %+v", deleted, created)
	}

	if _, err := q.GetVideo(ctx, created.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("GetVideo after delete: err = %v, want pgx.ErrNoRows", err)
	}
}

func TestDeleteVideoNotFoundReturnsErrNoRows(t *testing.T) {
	ctx := context.Background()
	q := testQueries(t)
	if _, err := q.DeleteVideo(ctx, uuid.New()); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("err = %v, want pgx.ErrNoRows", err)
	}
}
