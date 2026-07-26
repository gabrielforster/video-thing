package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gabrielforster/video-thing/packages/database/db"
)

var errNotUploading = errors.New("video is not in the uploading state")

type pgStore struct {
	*db.Queries
	pool *pgxpool.Pool
}

func newPGStore(pool *pgxpool.Pool) *pgStore {
	return &pgStore{Queries: db.New(pool), pool: pool}
}

func (s *pgStore) CompleteUpload(ctx context.Context, id uuid.UUID) (db.Video, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return db.Video{}, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	q := s.Queries.WithTx(tx)

	if _, err := q.GetVideoForUpdate(ctx, id); err != nil {
		return db.Video{}, err
	}

	updated, err := q.MarkProcessingFromUploading(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Video{}, errNotUploading
	}
	if err != nil {
		return db.Video{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return db.Video{}, fmt.Errorf("commit: %w", err)
	}
	return updated, nil
}
