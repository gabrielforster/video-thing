package main

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gabrielforster/video-thing/packages/database/db"
)

func testPGStore(t *testing.T) *pgStore {
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
	return newPGStore(pool)
}

func createUploadingVideo(t *testing.T, s *pgStore) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := s.CreateVideo(context.Background(), db.CreateVideoParams{
		ID:           id,
		Title:        "complete upload test",
		SourceBucket: "video-thing-dev-raw-uploads",
		SourceKey:    "raw/" + id.String(),
	}); err != nil {
		t.Fatalf("CreateVideo: %v", err)
	}
	return id
}

func TestCompleteUploadSerializesOnRowLock(t *testing.T) {
	s := testPGStore(t)
	ctx := context.Background()
	id := createUploadingVideo(t, s)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	holder := s.Queries.WithTx(tx)
	if _, err := holder.GetVideoForUpdate(ctx, id); err != nil {
		t.Fatalf("GetVideoForUpdate: %v", err)
	}

	result := make(chan error, 1)
	go func() {
		_, err := s.CompleteUpload(ctx, id)
		result <- err
	}()

	select {
	case err := <-result:
		t.Fatalf("CompleteUpload returned %v while the row lock was held; it did not serialize", err)
	case <-time.After(500 * time.Millisecond):
	}

	if _, err := holder.MarkProcessingFromUploading(ctx, id); err != nil {
		t.Fatalf("MarkProcessingFromUploading: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	select {
	case err := <-result:
		if !errors.Is(err, errNotUploading) {
			t.Fatalf("contender err = %v, want errNotUploading", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CompleteUpload did not return after the lock was released")
	}

	final, err := s.GetVideo(ctx, id)
	if err != nil {
		t.Fatalf("GetVideo: %v", err)
	}
	if final.Status != db.VideoStatusProcessing {
		t.Fatalf("final status = %q, want processing", final.Status)
	}
}

func TestCompleteUploadExactlyOneCallerWins(t *testing.T) {
	s := testPGStore(t)
	ctx := context.Background()
	id := createUploadingVideo(t, s)

	const callers = 8
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	start := make(chan struct{})
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := s.CompleteUpload(ctx, id)
			mu.Lock()
			errs = append(errs, err)
			mu.Unlock()
		}()
	}
	close(start)
	wg.Wait()

	var succeeded, conflicted int
	for _, err := range errs {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, errNotUploading):
			conflicted++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if succeeded != 1 || conflicted != callers-1 {
		t.Fatalf("succeeded = %d, conflicted = %d; want exactly 1 and %d", succeeded, conflicted, callers-1)
	}
}

func TestCompleteUploadNotFoundReturnsErrNoRows(t *testing.T) {
	s := testPGStore(t)
	if _, err := s.CompleteUpload(context.Background(), uuid.New()); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("err = %v, want pgx.ErrNoRows", err)
	}
}

func TestCompleteUploadRejectsNonUploadingStatus(t *testing.T) {
	s := testPGStore(t)
	ctx := context.Background()
	id := createUploadingVideo(t, s)

	if _, err := s.CompleteUpload(ctx, id); err != nil {
		t.Fatalf("first CompleteUpload: %v", err)
	}
	if _, err := s.CompleteUpload(ctx, id); !errors.Is(err, errNotUploading) {
		t.Fatalf("second CompleteUpload err = %v, want errNotUploading", err)
	}
}
