# Task 1: sqlc queries for listing, counting, and deleting

> Task 1 of 7 in [`api-list-delete`](00-context.md). Read [`00-context.md`](00-context.md) first — the goal, tech stack, Global Constraints, and file structure bind this task. Full plan: [`api-list-delete-plan.md`](../../plans/api-list-delete-plan.md).
>
> Next: [Task 2](task-02-get-videos-pagination.md)

---

**Files:**
- Modify: `packages/database/queries.sql`
- Modify (generated, do not hand-edit): `packages/database/db/` (`sqlc generate` regenerates `queries.sql.go`)
- Modify: `packages/database/db/queries_test.go`

**Interfaces:**
- Consumes: `db.Video`, `db.CreateVideoParams`, `db.Queries` (vertical slice, Task 1 of that plan).
- Produces: `db.ListVideosParams{Limit, Offset int32}`, `func (q *Queries) ListVideos(ctx, db.ListVideosParams) ([]db.Video, error)`, `func (q *Queries) CountVideos(ctx) (int64, error)`, `func (q *Queries) DeleteVideo(ctx, uuid.UUID) (db.Video, error)`. These exact signatures were confirmed by actually running `sqlc generate` against the current schema — sqlc infers `int32` for the `LIMIT`/`OFFSET` placeholders and `int64` for `COUNT(*)`.

`pgStore` (in `apps/api/store.go`) embeds `*db.Queries`, so once these three methods exist on `db.Queries`, `pgStore` satisfies them automatically — no changes to `store.go` are needed in this plan at all.

- [ ] **Step 1: Add the three queries**

Append to `packages/database/queries.sql`:

```sql

-- name: ListVideos :many
SELECT * FROM videos ORDER BY created_at DESC LIMIT $1 OFFSET $2;

-- name: CountVideos :one
SELECT COUNT(*) FROM videos;

-- name: DeleteVideo :one
DELETE FROM videos WHERE id = $1 RETURNING *;
```

- [ ] **Step 2: Write the failing query-layer tests**

Modify `packages/database/db/queries_test.go` to add a shared helper and the new tests. The full file, after this step:

```go
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
```

- [ ] **Step 2b: Run the tests to verify they fail to compile**

Run: `go test ./packages/database/... -run TestListVideos -v`
Expected: FAIL — `undefined: db.ListVideosParams` (the query doesn't exist yet, so the package doesn't compile).

- [ ] **Step 3: Regenerate the sqlc code**

```bash
cd /home/gabrielrocha/github/personal/video-thing
make sqlc
```

This rewrites `packages/database/db/queries.sql.go` to add (verified by running this exact command while writing this plan):

```go
const countVideos = `-- name: CountVideos :one
SELECT COUNT(*) FROM videos
`

func (q *Queries) CountVideos(ctx context.Context) (int64, error) {
	row := q.db.QueryRow(ctx, countVideos)
	var count int64
	err := row.Scan(&count)
	return count, err
}

const deleteVideo = `-- name: DeleteVideo :one
DELETE FROM videos WHERE id = $1 RETURNING id, title, status, duration, width, height, size_bytes, master_playlist, thumbnail, source_bucket, source_key, error_message, created_at, updated_at
`

func (q *Queries) DeleteVideo(ctx context.Context, id uuid.UUID) (Video, error) {
	row := q.db.QueryRow(ctx, deleteVideo, id)
	var i Video
	err := row.Scan(
		&i.ID, &i.Title, &i.Status, &i.Duration, &i.Width, &i.Height, &i.SizeBytes,
		&i.MasterPlaylist, &i.Thumbnail, &i.SourceBucket, &i.SourceKey, &i.ErrorMessage,
		&i.CreatedAt, &i.UpdatedAt,
	)
	return i, err
}

const listVideos = `-- name: ListVideos :many
SELECT id, title, status, duration, width, height, size_bytes, master_playlist, thumbnail, source_bucket, source_key, error_message, created_at, updated_at FROM videos ORDER BY created_at DESC LIMIT $1 OFFSET $2
`

type ListVideosParams struct {
	Limit  int32
	Offset int32
}

func (q *Queries) ListVideos(ctx context.Context, arg ListVideosParams) ([]Video, error) {
	rows, err := q.db.Query(ctx, listVideos, arg.Limit, arg.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Video
	for rows.Next() {
		var i Video
		if err := rows.Scan(
			&i.ID, &i.Title, &i.Status, &i.Duration, &i.Width, &i.Height, &i.SizeBytes,
			&i.MasterPlaylist, &i.Thumbnail, &i.SourceBucket, &i.SourceKey, &i.ErrorMessage,
			&i.CreatedAt, &i.UpdatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}
```

Do not type any of this by hand — `sqlc generate` produces it. If your output differs in formatting only, that's fine; if a type differs (e.g. `int64` instead of `int32` for `Limit`/`Offset`), trust the generator and update every reference in later tasks to match.

- [ ] **Step 4: Run the tests**

Run: `DATABASE_URL="postgres://user:userpassword@localhost:5432/videothing?sslmode=disable" go test ./packages/database/... -v` (requires `make up` to have run first)
Expected: PASS for all of `TestListVideosOrdersByCreatedAtDescWithLimitAndOffset`, `TestCountVideosCountsAllRows`, `TestDeleteVideoRemovesRowAndReturnsIt`, `TestDeleteVideoNotFoundReturnsErrNoRows`, plus the two pre-existing tests.

Run: `go test ./...`
Expected: PASS (other packages skip the DB tests when `DATABASE_URL` is unset in that shell).

- [ ] **Step 5: Commit**

```bash
git add packages/database/queries.sql packages/database/db packages/database/db/queries_test.go
git commit -m "feat: add ListVideos, CountVideos, and DeleteVideo queries"
```

---
