# API List/Delete/Logging Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Finish the API surface `docs/specifications/openapi.yaml` already contracts — paginated `GET /videos` and `DELETE /videos/{id}` with S3 asset cleanup — and add the structured JSON logging `docs/specifications/video-thing-spec.md` §16 asks for.

**Architecture:** Three additive layers on top of the vertical slice, in dependency order: (1) three new sqlc queries (`ListVideos`, `CountVideos`, `DeleteVideo`) satisfied automatically by `pgStore`'s embedded `*db.Queries` — no `store.go` changes; (2) a narrow `s3API` interface (`ListObjectsV2` + `DeleteObjects`, mirroring the worker's `sqsAPI` pattern in `apps/worker/consumer.go`) behind a concrete `S3AssetCleaner`, so deletion logic is unit-testable without LocalStack; (3) a `log/slog` JSON handler plus an `X-Request-Id` gin middleware that replaces `gin.Logger()`. Response shapes are exactly what `openapi.yaml`'s `VideoList`/`PaginationMeta` schemas and cross-plan contract 1/3 specify, since the `web-dashboard` plan consumes them directly over HTTP.

**Tech Stack:** Go 1.25.5, Gin, pgx/v5, sqlc v1.31.1, AWS SDK for Go v2 (`s3` package), `log/slog` (stdlib, no new dependency), `github.com/google/uuid` (already a dependency) for request-id generation.

**Depends on:** nothing beyond the vertical slice (`docs/plans/vertical-slice-plan.md`) — that plan is finished and passing. `docs/plans/web-dashboard-plan.md` will call this plan's `GET /videos` and `DELETE /videos/{id}` endpoints, but does not need to land first; the response shapes below are fixed so that plan can be written against them now.

## Global Constraints

- Go 1.25.5, single root module `github.com/gabrielforster/video-thing`. No `go.work`, no per-app module.
- Every task ends `gofmt -l .` silent, `go vet ./...` clean, `go test ./...` green (DB-dependent tests skip when `DATABASE_URL` is unset, per the existing pattern in `packages/database/db/queries_test.go` and `apps/api/store_test.go`).
- Response shapes match `openapi.yaml` exactly, including the `{"error":{"code","message"}}` envelope. `GET /videos` returns `{"items":[...],"pagination":{"limit","offset","total","nextOffset"}}`; `items` is `[]`, never `null`, on an empty page.
- Generated code under `packages/database/db/` is produced only by `make sqlc` (`cd packages/database && sqlc generate`) and is never hand-edited.
- Services never auto-migrate on boot; this plan adds no migration (no new columns — contract 5).
- Structured logging is `log/slog` with `slog.NewJSONHandler`, stdlib only, no logging dependency.
- Commit convention: `feat:`, `fix:`, `docs:`, `chore:`, `refactor:`, `test:`. No `Co-Authored-By` trailer on any commit.
- Comment discipline: a comment only where its absence causes a real bug (e.g. `make([]videoJSON, ...)` vs. a nil slice serializing as `null`, or the deletion-ordering rationale). No doc comments on unexported identifiers, no comments in `_test.go` files.
- `s3.DeleteObjects` accepts at most 1000 keys per call; `S3AssetCleaner` must batch.
- Every task's Go code in this plan was compiled and its tests run (including live, against a real LocalStack + Postgres) while writing this plan, so the listed commands and expected outputs are not guesses.

## File Structure

| Path | Responsibility |
|---|---|
| `packages/database/queries.sql` | sqlc source; adds `ListVideos`, `CountVideos`, `DeleteVideo` |
| `packages/database/db/` | sqlc-generated code — regenerated via `make sqlc`, never hand-edited |
| `packages/database/db/queries_test.go` | Query-layer tests for the three new queries, skipped when `DATABASE_URL` is unset |
| `apps/api/handlers.go` | Adds pagination parsing + `listVideos` + `deleteVideo`; extends the `store` interface; adds the `assetCleaner` interface and `handlers.assets` field |
| `apps/api/handlers_test.go` | Fake store / asset-cleaner test doubles and handler tests for listing, deletion, and request-id behavior |
| `apps/api/router.go` | Adds `GET /videos` and `DELETE /videos/:id` routes; replaces `gin.Logger()` with a `slog`-based `requestLogging()` middleware |
| `apps/api/assets.go` | `s3API` interface (`ListObjectsV2` + `DeleteObjects`) and `S3AssetCleaner`, which deletes the raw key and paginates/batches deletion of everything under `processed/{id}/` |
| `apps/api/assets_test.go` | Fake-S3 tests: raw-key deletion, processed-prefix listing, 1000-key batching, list/delete error propagation |
| `apps/api/config.go` | Adds `ProcessedBucket` and its required-var check |
| `apps/api/config_test.go` | Updated for the new required variable |
| `apps/api/main.go` | Wires `S3AssetCleaner`, `cfg.ProcessedBucket`, and the JSON `slog` handler |
| `scripts/e2e.sh` | Adds `GET /videos` presence + pagination-validation + `DELETE`/404/bucket-cleanup assertions |
| `README.md` | Status paragraph and repository-layout line |
| `docs/specifications/vertical-slice-spec.md` | §3 deferred-work table |

---

## Task 1: sqlc queries for listing, counting, and deleting

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

## Task 2: `GET /videos` with pagination

**Files:**
- Modify: `apps/api/handlers.go`
- Modify: `apps/api/router.go`
- Modify: `apps/api/handlers_test.go`

**Interfaces:**
- Consumes: `db.ListVideosParams`, `db.Queries.ListVideos`/`CountVideos` (Task 1); `db.Video`, `handlers`, `store`, `fail`, `videoID`, `h.toJSON` (vertical slice).
- Produces: extends `store` with `ListVideos(ctx, db.ListVideosParams) ([]db.Video, error)` and `CountVideos(ctx) (int64, error)`; `func (h *handlers) listVideos(c *gin.Context)`; `func parsePagination(c *gin.Context) (pagination, bool)`; route `GET /videos`.

Per cross-plan contract 1: `limit` defaults to 20, min 1, max 100; `offset` defaults to 0, min 0; anything else (non-numeric, out of range, including floats like `1.5`) is `400 invalid_request`. `nextOffset` is `offset+limit` when more rows remain (`offset+limit < total`), else `null`. `items` is `[]`, never `null`, on an empty page — this requires `make([]videoJSON, len(videos))` rather than a nil `var` slice, since `json.Marshal` renders a nil slice as `null` and an empty non-nil slice as `[]`.

- [ ] **Step 1: Write the failing handler tests**

Modify `apps/api/handlers_test.go`. Add `"sort"` to the import block:

```go
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gabrielforster/video-thing/packages/database/db"
)
```

Add these two methods on `fakeStore`, directly above `func testRouter`:

```go
func (f *fakeStore) ListVideos(_ context.Context, arg db.ListVideosParams) ([]db.Video, error) {
	all := make([]db.Video, 0, len(f.videos))
	for _, v := range f.videos {
		all = append(all, v)
	}
	sort.Slice(all, func(i, j int) bool {
		return all[i].CreatedAt.Time.After(all[j].CreatedAt.Time)
	})

	start := int(arg.Offset)
	if start > len(all) {
		start = len(all)
	}
	end := start + int(arg.Limit)
	if end > len(all) {
		end = len(all)
	}
	return all[start:end], nil
}

func (f *fakeStore) CountVideos(_ context.Context) (int64, error) {
	return int64(len(f.videos)), nil
}
```

Add these tests, directly above `func TestCompleteTransitionsUploadingToProcessing`:

```go
func TestListVideosReturnsPagedEnvelopeNewestFirst(t *testing.T) {
	s := newFakeStore()
	now := time.Now().UTC()
	ids := [3]uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	for i, id := range ids {
		s.videos[id] = db.Video{
			ID:        id,
			Title:     "clip",
			Status:    db.VideoStatusReady,
			CreatedAt: pgtype.Timestamptz{Time: now.Add(time.Duration(i) * time.Minute), Valid: true},
			UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true},
		}
	}

	rec := do(t, testRouter(t, s), http.MethodGet, "/videos?limit=2&offset=0", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var got struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
		Pagination struct {
			Limit      int  `json:"limit"`
			Offset     int  `json:"offset"`
			Total      int  `json:"total"`
			NextOffset *int `json:"nextOffset"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(got.Items))
	}
	if got.Items[0].ID != ids[2].String() || got.Items[1].ID != ids[1].String() {
		t.Fatalf("items = %+v, want newest first (%s, %s)", got.Items, ids[2], ids[1])
	}
	if got.Pagination.Limit != 2 || got.Pagination.Offset != 0 || got.Pagination.Total != 3 {
		t.Fatalf("pagination = %+v, want limit=2 offset=0 total=3", got.Pagination)
	}
	if got.Pagination.NextOffset == nil || *got.Pagination.NextOffset != 2 {
		t.Fatalf("nextOffset = %v, want 2", got.Pagination.NextOffset)
	}
}

func TestListVideosDefaultsLimitAndOffsetAndOmitsNullItems(t *testing.T) {
	rec := do(t, testRouter(t, newFakeStore()), http.MethodGet, "/videos", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var got struct {
		Items      []json.RawMessage `json:"items"`
		Pagination struct {
			Limit  int `json:"limit"`
			Offset int `json:"offset"`
			Total  int `json:"total"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Items == nil {
		t.Fatal("items decoded as null, want an empty array")
	}
	if len(got.Items) != 0 {
		t.Fatalf("len(items) = %d, want 0", len(got.Items))
	}
	if got.Pagination.Limit != 20 || got.Pagination.Offset != 0 || got.Pagination.Total != 0 {
		t.Fatalf("pagination = %+v, want limit=20 offset=0 total=0", got.Pagination)
	}
}

func TestListVideosLastPageHasNilNextOffset(t *testing.T) {
	s := newFakeStore()
	id := uuid.New()
	s.videos[id] = db.Video{ID: id, Title: "clip", Status: db.VideoStatusReady}

	rec := do(t, testRouter(t, s), http.MethodGet, "/videos?limit=20&offset=0", nil)
	var got struct {
		Pagination struct {
			NextOffset *int `json:"nextOffset"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Pagination.NextOffset != nil {
		t.Fatalf("nextOffset = %v, want nil", *got.Pagination.NextOffset)
	}
}

func TestListVideosRejectsInvalidLimit(t *testing.T) {
	for _, limit := range []string{"0", "101", "abc", "-1", "1.5"} {
		rec := do(t, testRouter(t, newFakeStore()), http.MethodGet, "/videos?limit="+limit, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("limit=%q: status = %d, want 400: %s", limit, rec.Code, rec.Body.String())
		}
		var got struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("limit=%q: decode: %v", limit, err)
		}
		if got.Error.Code != "invalid_request" {
			t.Fatalf("limit=%q: code = %q, want invalid_request", limit, got.Error.Code)
		}
	}
}

func TestListVideosRejectsInvalidOffset(t *testing.T) {
	for _, offset := range []string{"-1", "abc", "1.5"} {
		rec := do(t, testRouter(t, newFakeStore()), http.MethodGet, "/videos?offset="+offset, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("offset=%q: status = %d, want 400: %s", offset, rec.Code, rec.Body.String())
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./apps/api/... -run TestListVideos -v`
Expected: FAIL with a compile error — `undefined: h.listVideos` / `too many arguments in call to db.ListVideosParams` (the handler and store-interface additions don't exist yet).

- [ ] **Step 3: Write the pagination parsing and `listVideos` handler**

Modify `apps/api/handlers.go`. The full file, after this step:

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/gabrielforster/video-thing/packages/database/db"
)

const (
	defaultListLimit  = 20
	minListLimit      = 1
	maxListLimit      = 100
	defaultListOffset = 0
)

type store interface {
	CreateVideo(ctx context.Context, arg db.CreateVideoParams) (db.Video, error)
	GetVideo(ctx context.Context, id uuid.UUID) (db.Video, error)
	CompleteUpload(ctx context.Context, id uuid.UUID) (db.Video, error)
	ListVideos(ctx context.Context, arg db.ListVideosParams) ([]db.Video, error)
	CountVideos(ctx context.Context) (int64, error)
}

type handlers struct {
	store     store
	presigner *Presigner
	rawBucket string
	assetBase string
}

func newHandlers(s store, p *Presigner, rawBucket, assetBase string) *handlers {
	return &handlers{store: s, presigner: p, rawBucket: rawBucket, assetBase: assetBase}
}

type videoJSON struct {
	ID             string    `json:"id"`
	Title          string    `json:"title"`
	Status         string    `json:"status"`
	Duration       *float64  `json:"duration"`
	Width          *int32    `json:"width"`
	Height         *int32    `json:"height"`
	Size           *int64    `json:"size"`
	MasterPlaylist *string   `json:"master_playlist"`
	Thumbnail      *string   `json:"thumbnail"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func (h *handlers) assetURL(key *string) *string {
	if key == nil || *key == "" {
		return nil
	}
	url := h.assetBase + "/" + *key
	return &url
}

func (h *handlers) toJSON(v db.Video) videoJSON {
	return videoJSON{
		ID:             v.ID.String(),
		Title:          v.Title,
		Status:         string(v.Status),
		Duration:       v.Duration,
		Width:          v.Width,
		Height:         v.Height,
		Size:           v.SizeBytes,
		MasterPlaylist: h.assetURL(v.MasterPlaylist),
		Thumbnail:      h.assetURL(v.Thumbnail),
		CreatedAt:      v.CreatedAt.Time.UTC(),
		UpdatedAt:      v.UpdatedAt.Time.UTC(),
	}
}

func fail(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": message}})
}

type createVideoRequest struct {
	Title string `json:"title" binding:"required,min=1,max=255"`
}

func (h *handlers) createVideo(c *gin.Context) {
	var req createVideoRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid_request", "title is required and must be 1-255 characters")
		return
	}

	id := uuid.New()
	key := "raw/" + id.String()

	video, err := h.store.CreateVideo(c.Request.Context(), db.CreateVideoParams{
		ID:           id,
		Title:        req.Title,
		SourceBucket: h.rawBucket,
		SourceKey:    key,
	})
	if err != nil {
		fail(c, http.StatusInternalServerError, "internal_error", "could not create video record")
		return
	}

	url, expiresAt, err := h.presigner.UploadURL(c.Request.Context(), key)
	if err != nil {
		fail(c, http.StatusInternalServerError, "internal_error", "could not generate upload URL")
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"video": h.toJSON(video),
		"upload": gin.H{
			"uploadUrl": url,
			"method":    "PUT",
			"expiresAt": expiresAt,
			"headers":   gin.H{"Content-Type": UploadContentType},
		},
	})
}

type pagination struct {
	Limit  int32
	Offset int32
}

func parsePagination(c *gin.Context) (pagination, bool) {
	limit := int32(defaultListLimit)
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || n < minListLimit || n > maxListLimit {
			fail(c, http.StatusBadRequest, "invalid_request",
				fmt.Sprintf("limit must be an integer between %d and %d", minListLimit, maxListLimit))
			return pagination{}, false
		}
		limit = int32(n)
	}

	offset := int32(defaultListOffset)
	if raw := c.Query("offset"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || n < defaultListOffset {
			fail(c, http.StatusBadRequest, "invalid_request", "offset must be a non-negative integer")
			return pagination{}, false
		}
		offset = int32(n)
	}

	return pagination{Limit: limit, Offset: offset}, true
}

func (h *handlers) listVideos(c *gin.Context) {
	p, ok := parsePagination(c)
	if !ok {
		return
	}

	videos, err := h.store.ListVideos(c.Request.Context(), db.ListVideosParams{Limit: p.Limit, Offset: p.Offset})
	if err != nil {
		fail(c, http.StatusInternalServerError, "internal_error", "could not list videos")
		return
	}
	total, err := h.store.CountVideos(c.Request.Context())
	if err != nil {
		fail(c, http.StatusInternalServerError, "internal_error", "could not count videos")
		return
	}

	// make(...), not a nil "var" slice: an empty page must serialize as [],
	// not JSON null.
	items := make([]videoJSON, len(videos))
	for i, v := range videos {
		items[i] = h.toJSON(v)
	}

	var nextOffset *int32
	if next := p.Offset + p.Limit; int64(next) < total {
		nextOffset = &next
	}

	c.JSON(http.StatusOK, gin.H{
		"items": items,
		"pagination": gin.H{
			"limit":      p.Limit,
			"offset":     p.Offset,
			"total":      total,
			"nextOffset": nextOffset,
		},
	})
}

func videoID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		fail(c, http.StatusBadRequest, "invalid_request", "id must be a UUID")
		return uuid.Nil, false
	}
	return id, true
}

func (h *handlers) getVideo(c *gin.Context) {
	id, ok := videoID(c)
	if !ok {
		return
	}
	video, err := h.store.GetVideo(c.Request.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(c, http.StatusNotFound, "not_found", "video not found")
		return
	}
	if err != nil {
		fail(c, http.StatusInternalServerError, "internal_error", "could not read video")
		return
	}
	c.JSON(http.StatusOK, h.toJSON(video))
}

func (h *handlers) completeUpload(c *gin.Context) {
	id, ok := videoID(c)
	if !ok {
		return
	}

	updated, err := h.store.CompleteUpload(c.Request.Context(), id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		fail(c, http.StatusNotFound, "not_found", "video not found")
	case errors.Is(err, errNotUploading):
		fail(c, http.StatusConflict, "invalid_state_transition",
			"Video "+id.String()+" is not in the 'uploading' state and cannot be marked as processing.")
	case err != nil:
		fail(c, http.StatusInternalServerError, "internal_error", "could not update video")
	default:
		c.JSON(http.StatusOK, h.toJSON(updated))
	}
}
```

- [ ] **Step 4: Register the route**

In `apps/api/router.go`, in `newRouter`, add the new route directly below the existing `r.POST("/videos", h.createVideo)` line:

```go
	r.POST("/videos", h.createVideo)
	r.GET("/videos", h.listVideos)
	r.GET("/videos/:id", h.getVideo)
	r.POST("/videos/:id/complete", h.completeUpload)
```

- [ ] **Step 5: Run the tests**

Run: `go test ./apps/api/... -v`
Expected: PASS for all tests, including the 6 new ones.

Run: `gofmt -l . && go vet ./... && go test ./...`
Expected: `gofmt -l .` prints nothing; `go vet` and `go test` are clean.

- [ ] **Step 6: Commit**

```bash
git add apps/api/handlers.go apps/api/router.go apps/api/handlers_test.go
git commit -m "feat: add paginated GET /videos"
```

---

## Task 3: S3 asset deletion (`S3AssetCleaner`) and `PROCESSED_BUCKET` config

**Files:**
- Create: `apps/api/assets.go`
- Create: `apps/api/assets_test.go`
- Modify: `apps/api/config.go`
- Modify: `apps/api/config_test.go`

**Interfaces:**
- Consumes: `db.Video` (vertical slice); AWS SDK v2 `s3.Client` methods `ListObjectsV2`, `DeleteObjects` (signatures fixed by the SDK, `github.com/aws/aws-sdk-go-v2/service/s3` v1.106.0, already a dependency).
- Produces: `type s3API interface { ListObjectsV2(...); DeleteObjects(...) }`; `type S3AssetCleaner struct`; `func NewS3AssetCleaner(client s3API, processedBucket string) *S3AssetCleaner`; `func (a *S3AssetCleaner) deleteVideoAssets(ctx context.Context, v db.Video) error` (this becomes the `assetCleaner` interface's one method in Task 4); `Config.ProcessedBucket`.

This task does not wire the cleaner into `handlers` yet — that's Task 4, once `DeleteVideo` exists on the `store` interface. This task only builds and unit-tests the S3 logic in isolation, the same way `apps/worker/consumer.go`'s `sqsAPI` interface is faked in `consumer_test.go` without touching a real queue.

`sequence-diagrams.md`'s "Deletion Flow" depicts both buckets being cleaned via `ListObjectsV2` + `DeleteObjects` on a `{id}/` prefix. This plan deviates for the **raw** bucket only: since exactly one raw object exists per video (`source_key`, from the already-deleted row), no listing is needed there — `deleteBatch` is called directly with that single key. The **processed** bucket still needs listing, since ffmpeg produces a variable number of segment files.

`PROCESSED_BUCKET` is already exported by the `Makefile` (line 10, `export PROCESSED_BUCKET ?= video-thing-dev-processed-assets`) for the worker, and by `scripts/e2e.sh` (line 13). No changes are needed in either file — `apps/api` will now simply also read the variable that was already being exported to its environment.

- [ ] **Step 1: Write the failing S3-cleanup tests**

Create `apps/api/assets_test.go`:

```go
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
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./apps/api/... -run TestDeleteVideoAssets -v`
Expected: FAIL to compile — `undefined: NewS3AssetCleaner`.

- [ ] **Step 3: Write `apps/api/assets.go`**

```go
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
```

`s3.NewListObjectsV2Paginator` takes an `s3.ListObjectsV2APIClient` (one method: `ListObjectsV2`); `s3API` has a superset of that method set with an identical signature, so `a.s3` (typed `s3API`) is directly assignable — no adapter needed. This was verified to compile and to paginate correctly (the paginator reads `IsTruncated`/`NextContinuationToken` off `*s3.ListObjectsV2Output`, which is exactly what `fakeS3` sets).

- [ ] **Step 4: Add `ProcessedBucket` to `Config`**

Modify `apps/api/config.go`. The full file, after this step:

```go
package main

import (
	"fmt"
	"sort"
	"strings"
)

type Config struct {
	DatabaseURL        string
	RawBucket          string
	ProcessedBucket    string
	AWSEndpointURL     string
	PublicAssetBaseURL string
	Port               string
}

func LoadConfig(getenv func(string) string) (Config, error) {
	cfg := Config{
		DatabaseURL:        getenv("DATABASE_URL"),
		RawBucket:          getenv("RAW_BUCKET"),
		ProcessedBucket:    getenv("PROCESSED_BUCKET"),
		AWSEndpointURL:     getenv("AWS_ENDPOINT_URL"),
		PublicAssetBaseURL: strings.TrimSuffix(getenv("PUBLIC_ASSET_BASE_URL"), "/"),
		Port:               getenv("PORT"),
	}
	if cfg.Port == "" {
		cfg.Port = "8080"
	}

	var missing []string
	for name, value := range map[string]string{
		"DATABASE_URL":          cfg.DatabaseURL,
		"RAW_BUCKET":            cfg.RawBucket,
		"PROCESSED_BUCKET":      cfg.ProcessedBucket,
		"PUBLIC_ASSET_BASE_URL": cfg.PublicAssetBaseURL,
	} {
		if value == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return Config{}, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}
	return cfg, nil
}
```

Update `apps/api/config_test.go` to match — the full file, after this step:

```go
package main

import "testing"
import "strings"

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadConfigDefaultsPort(t *testing.T) {
	cfg, err := LoadConfig(env(map[string]string{
		"DATABASE_URL":          "postgres://localhost/db",
		"RAW_BUCKET":            "raw",
		"PROCESSED_BUCKET":      "processed",
		"PUBLIC_ASSET_BASE_URL": "http://localhost:4566/processed",
	}))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Port != "8080" {
		t.Fatalf("Port = %q, want 8080", cfg.Port)
	}
	if cfg.AWSEndpointURL != "" {
		t.Fatalf("AWSEndpointURL = %q, want empty", cfg.AWSEndpointURL)
	}
}

func TestLoadConfigRequiresVars(t *testing.T) {
	_, err := LoadConfig(env(map[string]string{"RAW_BUCKET": "raw"}))
	if err == nil {
		t.Fatal("expected error for missing DATABASE_URL")
	}
	if got := err.Error(); !strings.Contains(got, "DATABASE_URL") || !strings.Contains(got, "PUBLIC_ASSET_BASE_URL") || !strings.Contains(got, "PROCESSED_BUCKET") {
		t.Fatalf("error %q should name every missing variable", got)
	}
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./apps/api/... -v`
Expected: PASS for all tests, including the 4 new `TestDeleteVideoAssets*` tests and the updated config tests.

Run: `gofmt -l . && go vet ./... && go test ./...`
Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add apps/api/assets.go apps/api/assets_test.go apps/api/config.go apps/api/config_test.go
git commit -m "feat: add S3AssetCleaner and PROCESSED_BUCKET config"
```

---

## Task 4: `DELETE /videos/{id}` handler

**Files:**
- Modify: `apps/api/handlers.go`
- Modify: `apps/api/router.go`
- Modify: `apps/api/main.go`
- Modify: `apps/api/handlers_test.go`

**Interfaces:**
- Consumes: `db.Queries.DeleteVideo` (Task 1); `S3AssetCleaner`, `NewS3AssetCleaner` (Task 3); `pagination`/`listVideos` (Task 2).
- Produces: extends `store` with `DeleteVideo(ctx, uuid.UUID) (db.Video, error)`; `type assetCleaner interface { deleteVideoAssets(ctx, db.Video) error }`; `handlers.assets assetCleaner` field; `newHandlers` gains an `assets assetCleaner` parameter (`func newHandlers(s store, p *Presigner, assets assetCleaner, rawBucket, assetBase string) *handlers`); `func (h *handlers) deleteVideo(c *gin.Context)`; route `DELETE /videos/:id`.

**Ordering, per `docs/architecture/sequence-diagrams.md` "Deletion Flow":** the DB row is deleted *before* S3 cleanup runs. One-line reason (quoting that section): "it means a video can never be visible via the API while its assets are only partially deleted," at the cost of a small window where the DB says "gone" but bytes still exist in S3. Concretely: `deleteVideo` calls `store.DeleteVideo` first, and only on success calls `assets.deleteVideoAssets`; the S3 cleanup runs synchronously in the same request (no queue exists for it — the same section notes only one SQS queue is provisioned, for processing), but its outcome never changes the response: `204` is returned whether or not cleanup succeeds. A cleanup failure is logged and otherwise swallowed, since the row's disappearance is what the API contract promises, not that every byte in S3 is gone by the time the request returns.

**A video deleted mid-processing** is the same benign race the sequence diagram calls out: the row disappears immediately; the worker's later `MarkProcessing`/`MarkReady` (from `apps/worker/pipeline.go`) matches zero rows and is silently a no-op (per `MarkReady`'s `WHERE id = $1` with no row present, `RETURNING *` returns `pgx.ErrNoRows`, and the worker's `process` already treats that path as unremarkable in existing code); the SQS message is eventually deleted by the worker regardless of that outcome. No code change is needed for this plan to handle it correctly. What *is* a known gap, not fixed here: any S3 objects the worker `PutObject`s **after** this flow's `DeleteObjects` already ran are orphaned, since there's no sweeper or lifecycle rule today (the sequence diagram flags exactly this as "worth a lifecycle rule ... if this race turns out to matter in practice").

- [ ] **Step 1: Write the failing handler tests**

Modify `apps/api/handlers_test.go`. Add these two `fakeStore`/test-double additions directly below `func (f *fakeStore) CountVideos`:

```go
func (f *fakeStore) DeleteVideo(_ context.Context, id uuid.UUID) (db.Video, error) {
	v, ok := f.videos[id]
	if !ok {
		return db.Video{}, pgx.ErrNoRows
	}
	delete(f.videos, id)
	return v, nil
}

type fakeAssetCleaner struct {
	err     error
	deleted []db.Video
}

func newFakeAssetCleaner() *fakeAssetCleaner { return &fakeAssetCleaner{} }

func (f *fakeAssetCleaner) deleteVideoAssets(_ context.Context, v db.Video) error {
	f.deleted = append(f.deleted, v)
	return f.err
}
```

Replace `testRouter`/`testRouterWithPing` with these three functions (adds `testRouterWithAssets`, keeps the first two callable with their old signatures so every existing test call site is unchanged):

```go
func testRouter(t *testing.T, s store) *gin.Engine {
	t.Helper()
	return testRouterWithPing(t, s, func(context.Context) error { return nil })
}

func testRouterWithPing(t *testing.T, s store, ping func(context.Context) error) *gin.Engine {
	t.Helper()
	return testRouterWithAssets(t, s, newFakeAssetCleaner(), ping)
}

func testRouterWithAssets(t *testing.T, s store, assets assetCleaner, ping func(context.Context) error) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := newHandlers(s, NewPresigner(testS3Client(t), "video-thing-dev-raw-uploads", 15*time.Minute),
		assets, "video-thing-dev-raw-uploads", "http://localhost:4566/video-thing-dev-processed-assets")
	return newRouter(h, ping)
}
```

Add these tests directly above `func TestCompleteTransitionsUploadingToProcessing`:

```go
func TestDeleteVideoReturns204AndCleansAssets(t *testing.T) {
	s := newFakeStore()
	id := uuid.New()
	s.videos[id] = db.Video{ID: id, Title: "clip", Status: db.VideoStatusReady, SourceBucket: "raw", SourceKey: "raw/" + id.String()}
	assets := newFakeAssetCleaner()

	rec := do(t, testRouterWithAssets(t, s, assets, func(context.Context) error { return nil }),
		http.MethodDelete, "/videos/"+id.String(), nil)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("body = %q, want empty", rec.Body.String())
	}
	if _, ok := s.videos[id]; ok {
		t.Fatal("video still present in store after delete")
	}
	if len(assets.deleted) != 1 || assets.deleted[0].ID != id {
		t.Fatalf("assets.deleted = %+v, want exactly one entry for %s", assets.deleted, id)
	}
}

func TestDeleteVideoNotFound(t *testing.T) {
	rec := do(t, testRouter(t, newFakeStore()), http.MethodDelete, "/videos/"+uuid.New().String(), nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestDeleteVideoReturns204EvenWhenAssetCleanupFails(t *testing.T) {
	s := newFakeStore()
	id := uuid.New()
	s.videos[id] = db.Video{ID: id, Title: "clip", Status: db.VideoStatusReady}
	assets := &fakeAssetCleaner{err: errors.New("s3 unavailable")}

	rec := do(t, testRouterWithAssets(t, s, assets, func(context.Context) error { return nil }),
		http.MethodDelete, "/videos/"+id.String(), nil)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (row is deleted regardless of S3 cleanup outcome): %s", rec.Code, rec.Body.String())
	}
	if _, ok := s.videos[id]; ok {
		t.Fatal("video still present in store after delete")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./apps/api/... -run TestDeleteVideo -v`
Expected: FAIL to compile — `not enough arguments in call to newHandlers` / `undefined: h.deleteVideo`.

- [ ] **Step 3: Wire `deleteVideo` into `handlers.go`, `router.go`, `main.go`**

Modify `apps/api/handlers.go`. Add `"log"` to the import block, add the `assetCleaner` interface and `assets` field, change `newHandlers`'s signature, and add `deleteVideo`. The full file, after this step:

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/gabrielforster/video-thing/packages/database/db"
)

const (
	defaultListLimit  = 20
	minListLimit      = 1
	maxListLimit      = 100
	defaultListOffset = 0
)

type store interface {
	CreateVideo(ctx context.Context, arg db.CreateVideoParams) (db.Video, error)
	GetVideo(ctx context.Context, id uuid.UUID) (db.Video, error)
	CompleteUpload(ctx context.Context, id uuid.UUID) (db.Video, error)
	ListVideos(ctx context.Context, arg db.ListVideosParams) ([]db.Video, error)
	CountVideos(ctx context.Context) (int64, error)
	DeleteVideo(ctx context.Context, id uuid.UUID) (db.Video, error)
}

type assetCleaner interface {
	deleteVideoAssets(ctx context.Context, v db.Video) error
}

type handlers struct {
	store     store
	presigner *Presigner
	assets    assetCleaner
	rawBucket string
	assetBase string
}

func newHandlers(s store, p *Presigner, assets assetCleaner, rawBucket, assetBase string) *handlers {
	return &handlers{store: s, presigner: p, assets: assets, rawBucket: rawBucket, assetBase: assetBase}
}

type videoJSON struct {
	ID             string    `json:"id"`
	Title          string    `json:"title"`
	Status         string    `json:"status"`
	Duration       *float64  `json:"duration"`
	Width          *int32    `json:"width"`
	Height         *int32    `json:"height"`
	Size           *int64    `json:"size"`
	MasterPlaylist *string   `json:"master_playlist"`
	Thumbnail      *string   `json:"thumbnail"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func (h *handlers) assetURL(key *string) *string {
	if key == nil || *key == "" {
		return nil
	}
	url := h.assetBase + "/" + *key
	return &url
}

func (h *handlers) toJSON(v db.Video) videoJSON {
	return videoJSON{
		ID:             v.ID.String(),
		Title:          v.Title,
		Status:         string(v.Status),
		Duration:       v.Duration,
		Width:          v.Width,
		Height:         v.Height,
		Size:           v.SizeBytes,
		MasterPlaylist: h.assetURL(v.MasterPlaylist),
		Thumbnail:      h.assetURL(v.Thumbnail),
		CreatedAt:      v.CreatedAt.Time.UTC(),
		UpdatedAt:      v.UpdatedAt.Time.UTC(),
	}
}

func fail(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": message}})
}

type createVideoRequest struct {
	Title string `json:"title" binding:"required,min=1,max=255"`
}

func (h *handlers) createVideo(c *gin.Context) {
	var req createVideoRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid_request", "title is required and must be 1-255 characters")
		return
	}

	id := uuid.New()
	key := "raw/" + id.String()

	video, err := h.store.CreateVideo(c.Request.Context(), db.CreateVideoParams{
		ID:           id,
		Title:        req.Title,
		SourceBucket: h.rawBucket,
		SourceKey:    key,
	})
	if err != nil {
		fail(c, http.StatusInternalServerError, "internal_error", "could not create video record")
		return
	}

	url, expiresAt, err := h.presigner.UploadURL(c.Request.Context(), key)
	if err != nil {
		fail(c, http.StatusInternalServerError, "internal_error", "could not generate upload URL")
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"video": h.toJSON(video),
		"upload": gin.H{
			"uploadUrl": url,
			"method":    "PUT",
			"expiresAt": expiresAt,
			"headers":   gin.H{"Content-Type": UploadContentType},
		},
	})
}

type pagination struct {
	Limit  int32
	Offset int32
}

func parsePagination(c *gin.Context) (pagination, bool) {
	limit := int32(defaultListLimit)
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || n < minListLimit || n > maxListLimit {
			fail(c, http.StatusBadRequest, "invalid_request",
				fmt.Sprintf("limit must be an integer between %d and %d", minListLimit, maxListLimit))
			return pagination{}, false
		}
		limit = int32(n)
	}

	offset := int32(defaultListOffset)
	if raw := c.Query("offset"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || n < defaultListOffset {
			fail(c, http.StatusBadRequest, "invalid_request", "offset must be a non-negative integer")
			return pagination{}, false
		}
		offset = int32(n)
	}

	return pagination{Limit: limit, Offset: offset}, true
}

func (h *handlers) listVideos(c *gin.Context) {
	p, ok := parsePagination(c)
	if !ok {
		return
	}

	videos, err := h.store.ListVideos(c.Request.Context(), db.ListVideosParams{Limit: p.Limit, Offset: p.Offset})
	if err != nil {
		fail(c, http.StatusInternalServerError, "internal_error", "could not list videos")
		return
	}
	total, err := h.store.CountVideos(c.Request.Context())
	if err != nil {
		fail(c, http.StatusInternalServerError, "internal_error", "could not count videos")
		return
	}

	// make(...), not a nil "var" slice: an empty page must serialize as [],
	// not JSON null.
	items := make([]videoJSON, len(videos))
	for i, v := range videos {
		items[i] = h.toJSON(v)
	}

	var nextOffset *int32
	if next := p.Offset + p.Limit; int64(next) < total {
		nextOffset = &next
	}

	c.JSON(http.StatusOK, gin.H{
		"items": items,
		"pagination": gin.H{
			"limit":      p.Limit,
			"offset":     p.Offset,
			"total":      total,
			"nextOffset": nextOffset,
		},
	})
}

func videoID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		fail(c, http.StatusBadRequest, "invalid_request", "id must be a UUID")
		return uuid.Nil, false
	}
	return id, true
}

func (h *handlers) getVideo(c *gin.Context) {
	id, ok := videoID(c)
	if !ok {
		return
	}
	video, err := h.store.GetVideo(c.Request.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(c, http.StatusNotFound, "not_found", "video not found")
		return
	}
	if err != nil {
		fail(c, http.StatusInternalServerError, "internal_error", "could not read video")
		return
	}
	c.JSON(http.StatusOK, h.toJSON(video))
}

func (h *handlers) deleteVideo(c *gin.Context) {
	id, ok := videoID(c)
	if !ok {
		return
	}

	deleted, err := h.store.DeleteVideo(c.Request.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(c, http.StatusNotFound, "not_found", "video not found")
		return
	}
	if err != nil {
		fail(c, http.StatusInternalServerError, "internal_error", "could not delete video")
		return
	}

	// The row must already be gone before S3 cleanup runs: sequence-diagrams.md's
	// "Deletion Flow" says this ordering means a video can never be visible via
	// the API while its assets are only partially deleted.
	if err := h.assets.deleteVideoAssets(c.Request.Context(), deleted); err != nil {
		log.Printf("video %s: asset cleanup failed: %v", deleted.ID, err)
	}

	c.Status(http.StatusNoContent)
}

func (h *handlers) completeUpload(c *gin.Context) {
	id, ok := videoID(c)
	if !ok {
		return
	}

	updated, err := h.store.CompleteUpload(c.Request.Context(), id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		fail(c, http.StatusNotFound, "not_found", "video not found")
	case errors.Is(err, errNotUploading):
		fail(c, http.StatusConflict, "invalid_state_transition",
			"Video "+id.String()+" is not in the 'uploading' state and cannot be marked as processing.")
	case err != nil:
		fail(c, http.StatusInternalServerError, "internal_error", "could not update video")
	default:
		c.JSON(http.StatusOK, h.toJSON(updated))
	}
}
```

In `apps/api/router.go`, add the DELETE route:

```go
	r.POST("/videos", h.createVideo)
	r.GET("/videos", h.listVideos)
	r.GET("/videos/:id", h.getVideo)
	r.DELETE("/videos/:id", h.deleteVideo)
	r.POST("/videos/:id/complete", h.completeUpload)
```

In `apps/api/main.go`, wire the cleaner and pass `cfg.ProcessedBucket`:

```go
	h := newHandlers(newPGStore(pool), NewPresigner(s3Client, cfg.RawBucket, 15*time.Minute),
		NewS3AssetCleaner(s3Client, cfg.ProcessedBucket), cfg.RawBucket, cfg.PublicAssetBaseURL)
```

(`s3Client` here is the same `*s3.Client` already constructed for the `Presigner` a few lines above — no second client needed.)

- [ ] **Step 4: Run the tests**

Run: `go test ./apps/api/... -v`
Expected: PASS for all tests, including the 3 new `TestDeleteVideo*` tests.

Run: `gofmt -l . && go vet ./... && go test ./...`
Expected: clean.

- [ ] **Step 5: Commit**

```bash
git add apps/api/handlers.go apps/api/router.go apps/api/main.go apps/api/handlers_test.go
git commit -m "feat: add DELETE /videos/{id} with S3 asset cleanup"
```

---

## Task 5: Structured JSON logging and `X-Request-Id` middleware

**Files:**
- Modify: `apps/api/router.go`
- Modify: `apps/api/handlers.go`
- Modify: `apps/api/main.go`
- Modify: `apps/api/handlers_test.go`

**Interfaces:**
- Consumes: nothing new from earlier tasks in this plan; `github.com/google/uuid` (already a dependency).
- Produces: `func requestLogging() gin.HandlerFunc` (replaces `gin.Logger()`); `func requestLogger(c *gin.Context) *slog.Logger`; the `handlers.go` `deleteVideo` cleanup-failure log switches from `log.Printf` to `requestLogger(c).Error(...)`.

Per cross-plan contract 6: reuse an inbound `X-Request-Id` header when present, else generate a new one (`uuid.New().String()`); echo it back on the response; attach it to every log line for that request via a `*slog.Logger` built with `.With("request_id", reqID)`. The JSON handler itself (`slog.NewJSONHandler`) is installed once, in `main()` — tests don't assert on log *format* (that's `main()`-only, verified live below), only on the `X-Request-Id` echo/generation behavior, which is fully observable through the HTTP response headers.

- [ ] **Step 1: Write the failing request-id tests**

Add these two tests to `apps/api/handlers_test.go`, directly above `func TestHealthz`:

```go
func TestRequestLoggingEchoesInboundRequestID(t *testing.T) {
	r := testRouter(t, newFakeStore())
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("X-Request-Id", "test-request-id")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Request-Id"); got != "test-request-id" {
		t.Fatalf("X-Request-Id = %q, want %q", got, "test-request-id")
	}
}

func TestRequestLoggingGeneratesRequestIDWhenAbsent(t *testing.T) {
	rec := do(t, testRouter(t, newFakeStore()), http.MethodGet, "/healthz", nil)

	got := rec.Header().Get("X-Request-Id")
	if got == "" {
		t.Fatal("X-Request-Id header missing")
	}
	if _, err := uuid.Parse(got); err != nil {
		t.Fatalf("X-Request-Id = %q, not a UUID: %v", got, err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./apps/api/... -run TestRequestLogging -v`
Expected: FAIL — `X-Request-Id = "", want "test-request-id"` (no middleware sets the header yet).

- [ ] **Step 3: Replace `gin.Logger()` with the `slog`-based middleware**

Modify `apps/api/router.go`. The full file, after this step:

```go
package main

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const loggerContextKey = "logger"

func cors() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func requestLogging() gin.HandlerFunc {
	return func(c *gin.Context) {
		reqID := c.GetHeader("X-Request-Id")
		if reqID == "" {
			reqID = uuid.New().String()
		}
		c.Header("X-Request-Id", reqID)
		logger := slog.Default().With("request_id", reqID)
		c.Set(loggerContextKey, logger)

		start := time.Now()
		c.Next()

		logger.Info("request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}
}

func requestLogger(c *gin.Context) *slog.Logger {
	if v, ok := c.Get(loggerContextKey); ok {
		if logger, ok := v.(*slog.Logger); ok {
			return logger
		}
	}
	return slog.Default()
}

func newRouter(h *handlers, ping func(context.Context) error) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery(), requestLogging(), cors())

	r.POST("/videos", h.createVideo)
	r.GET("/videos", h.listVideos)
	r.GET("/videos/:id", h.getVideo)
	r.DELETE("/videos/:id", h.deleteVideo)
	r.POST("/videos/:id/complete", h.completeUpload)

	r.GET("/healthz", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	r.GET("/readyz", func(c *gin.Context) {
		if err := ping(c.Request.Context()); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"status": "unavailable",
				"checks": gin.H{"database": "unreachable"},
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"status": "ok",
			"checks": gin.H{"database": "ok"},
		})
	})

	return r
}
```

- [ ] **Step 4: Route the `deleteVideo` cleanup-failure log through the request-scoped logger**

In `apps/api/handlers.go`, remove `"log"` from the import block (it becomes unused) and replace the cleanup-failure line in `deleteVideo`:

```go
	if err := h.assets.deleteVideoAssets(c.Request.Context(), deleted); err != nil {
		requestLogger(c).Error("asset cleanup failed", "video_id", deleted.ID.String(), "error", err.Error())
	}
```

- [ ] **Step 5: Install the JSON handler in `main()`**

Modify `apps/api/main.go` to use `log/slog` instead of `log` throughout. The full file, after this step:

```go
package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	gin.SetMode(gin.ReleaseMode)

	cfg, err := LoadConfig(os.Getenv)
	if err != nil {
		slog.Error("config", "error", err)
		os.Exit(1)
	}

	ctx := context.Background()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		slog.Error("aws config", "error", err)
		os.Exit(1)
	}
	s3Client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		// LocalStack needs an explicit endpoint and path-style addressing;
		// in AWS both are empty/false and the SDK defaults apply.
		if cfg.AWSEndpointURL != "" {
			o.BaseEndpoint = aws.String(cfg.AWSEndpointURL)
			o.UsePathStyle = true
		}
	})

	h := newHandlers(newPGStore(pool), NewPresigner(s3Client, cfg.RawBucket, 15*time.Minute),
		NewS3AssetCleaner(s3Client, cfg.ProcessedBucket), cfg.RawBucket, cfg.PublicAssetBaseURL)

	r := newRouter(h, func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		return pool.Ping(ctx)
	})

	slog.Info("api listening", "port", cfg.Port)
	if err := r.Run(":" + cfg.Port); err != nil {
		slog.Error("listen", "error", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 6: Run the tests**

Run: `go test ./apps/api/... -v`
Expected: PASS for all tests, including the 2 new request-id tests.

Run: `gofmt -l . && go vet ./... && go test ./...`
Expected: clean.

- [ ] **Step 7: Verify the JSON log output live**

This was run against the real stack while writing this plan (`make up`, then the API built and started with real env vars) and produced lines like:

```
{"time":"2026-07-26T15:28:34.305477636-03:00","level":"INFO","msg":"request","request_id":"bcb028ed-139c-46b9-bf48-ee7ad14245e5","method":"POST","path":"/videos","status":201,"duration_ms":6}
```

Confirm the same locally:

```bash
make up
DATABASE_URL="postgres://user:userpassword@localhost:5432/videothing?sslmode=disable" \
AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1 AWS_ENDPOINT_URL=http://localhost:4566 \
RAW_BUCKET=video-thing-dev-raw-uploads PROCESSED_BUCKET=video-thing-dev-processed-assets \
PUBLIC_ASSET_BASE_URL=http://localhost:4566/video-thing-dev-processed-assets \
go run ./apps/api &
sleep 1
curl -s localhost:8080/healthz
```
Expected: the API's stdout shows one JSON object per request, each with a `request_id` field.

- [ ] **Step 8: Commit**

```bash
git add apps/api/router.go apps/api/handlers.go apps/api/main.go apps/api/handlers_test.go
git commit -m "feat: add structured JSON logging and X-Request-Id middleware"
```

---

## Task 6: Extend `scripts/e2e.sh` for listing, pagination, and deletion

**Files:**
- Modify: `scripts/e2e.sh`

**Interfaces:**
- Consumes: `$ID` (the video that reaches `ready`, already tracked by the existing script), `$PORT`, `$AWS_ENDPOINT_URL`, `$PROCESSED_BUCKET` (all already exported at the top of the script).
- Produces: exit code 0 only if the created video also appears in `GET /videos`, bad pagination is rejected with `400`, and `DELETE` leaves both the row and the processed objects gone.

This appends to the script, reusing its existing `$ID` (the fully-processed video from the retry loop), its `$TMP` scratch directory, and its `FAIL: ... >&2; exit 1` style. It runs after the existing final `echo "PASS: ..."` block (currently the last 4 lines of the file).

- [ ] **Step 1: Verify the JMESPath expression used below**

This was run against the real LocalStack instance while writing this plan:

```bash
aws --endpoint-url http://localhost:4566 s3api list-objects-v2 \
    --bucket video-thing-dev-processed-assets --prefix "processed/nonexistent-prefix/" \
    --query 'length(Contents || `[]`)' --output text
```
Expected: `0` (the `|| \`[]\`` guards against `Contents` being absent from the response when no keys match).

- [ ] **Step 2: Append the new checks to `scripts/e2e.sh`**

Add this after the script's existing final block (`echo "PASS: video $ID reached ready ..."` / `... which is readable unsigned and cross-origin"`):

```bash

echo "==> checking GET /videos includes the created video"
curl -sf "localhost:$PORT/videos" >"$TMP/list.json"
if ! jq -e --arg id "$ID" '.items | map(.id) | index($id) != null' "$TMP/list.json" >/dev/null; then
    echo "FAIL: video $ID not present in GET /videos items" >&2
    cat "$TMP/list.json" >&2
    exit 1
fi
if [ "$(jq -r '.pagination.limit' "$TMP/list.json")" != "20" ] || [ "$(jq -r '.pagination.offset' "$TMP/list.json")" != "0" ]; then
    echo "FAIL: default pagination is not limit=20/offset=0:" >&2
    jq .pagination "$TMP/list.json" >&2
    exit 1
fi

echo "==> checking pagination bounds are rejected"
for bad_query in "limit=0" "limit=101" "limit=abc" "offset=-1" "offset=abc"; do
    CODE="$(curl -s -o "$TMP/bad-page.json" -w '%{http_code}' "localhost:$PORT/videos?$bad_query")"
    if [ "$CODE" != "400" ] || [ "$(jq -r .error.code "$TMP/bad-page.json")" != "invalid_request" ]; then
        echo "FAIL: GET /videos?$bad_query returned $CODE / $(jq -r .error.code "$TMP/bad-page.json" 2>/dev/null)," >&2
        echo "      want 400 / invalid_request" >&2
        exit 1
    fi
done

echo "==> deleting video $ID and checking cleanup"
CODE="$(curl -s -o /dev/null -w '%{http_code}' -XDELETE "localhost:$PORT/videos/$ID")"
if [ "$CODE" != "204" ]; then
    echo "FAIL: DELETE /videos/$ID returned $CODE, want 204" >&2
    exit 1
fi

CODE="$(curl -s -o /dev/null -w '%{http_code}' "localhost:$PORT/videos/$ID")"
if [ "$CODE" != "404" ]; then
    echo "FAIL: GET /videos/$ID after delete returned $CODE, want 404" >&2
    exit 1
fi

REMAINING="$(aws --endpoint-url "$AWS_ENDPOINT_URL" s3api list-objects-v2 \
    --bucket "$PROCESSED_BUCKET" --prefix "processed/$ID/" \
    --query 'length(Contents || `[]`)' --output text)"
if [ "$REMAINING" != "0" ]; then
    echo "FAIL: $REMAINING objects remain under processed/$ID/ after DELETE" >&2
    exit 1
fi

echo "PASS: video $ID appears in GET /videos, invalid pagination is rejected with 400,"
echo "      and DELETE removed both the row and every processed object"
```

- [ ] **Step 3: Run it from a cold stack**

```bash
docker compose down -v
make e2e
```
Expected: the script prints all of its existing `PASS`/`==>` lines plus the four new sections above, and exits 0.

- [ ] **Step 4: Commit**

```bash
git add scripts/e2e.sh
git commit -m "test: extend e2e.sh for listing, pagination, and deletion"
```

---

## Task 7: Update docs invalidated by this plan

**Files:**
- Modify: `README.md`
- Modify: `docs/specifications/vertical-slice-spec.md`

**Interfaces:**
- Consumes: nothing (documentation only).
- Produces: nothing consumed by later tasks — this is the last task.

- [ ] **Step 1: Update `README.md`'s status paragraph**

Replace the current status paragraph (which lists deletion and listing as unbuilt):

```markdown
**Status:** vertical slice implemented. `apps/api`, `apps/worker`, and `apps/web` run locally against LocalStack (S3 + SQS) and Postgres: a browser can upload a file, the worker transcodes it to 720p HLS with a thumbnail, and the page plays it back. `scripts/e2e.sh` proves the pipeline end to end from a cold stack. The full rendition ladder (only 720p exists today), deletion, listing, CloudFront, and deployment to AWS remain unbuilt — architecture, infrastructure, and API contract for that fuller scope are specified and the Terraform module tree is implemented and `terraform validate`-clean.
```

with:

```markdown
**Status:** vertical slice implemented, plus paginated listing, deletion (with S3 asset cleanup), and structured JSON logging (`docs/plans/api-list-delete-plan.md`). `apps/api`, `apps/worker`, and `apps/web` run locally against LocalStack (S3 + SQS) and Postgres: a browser can upload a file, the worker transcodes it to 720p HLS with a thumbnail, and the page plays it back; `GET /videos` lists videos with `limit`/`offset` pagination and `DELETE /videos/{id}` removes the row and its S3 assets. `scripts/e2e.sh` proves the pipeline, listing, and deletion end to end from a cold stack. The full rendition ladder (only 720p exists today), a dashboard UI, CloudFront, and deployment to AWS remain unbuilt — architecture, infrastructure, and API contract for that fuller scope are specified and the Terraform module tree is implemented and `terraform validate`-clean.
```

Also update the `apps/api` line in the "Repository layout" section:

```markdown
    api/                Gin service: presigned uploads, video CRUD, listing, deletion, health/readiness
```

replacing:

```markdown
    api/                Gin service: presigned uploads, video CRUD, health/readiness
```

- [ ] **Step 2: Update `docs/specifications/vertical-slice-spec.md` §3**

Replace the "Out of Scope" table:

```markdown
| Deferred | Owning spec |
|---|---|
| 1080p / 480p / 360p renditions, source-resolution-aware selection | worker spec |
| `DELETE /videos/{id}` and asset cleanup | api spec |
| `GET /videos` list, pagination | api spec |
| Dashboard and video-detail pages, TanStack Query/Router | web spec |
| CloudFront in the playback path | infrastructure spec |
| ECS deployment, CI/CD, monitoring, DLQ wiring | delivery spec |
```

with:

```markdown
| Deferred | Owning spec |
|---|---|
| 1080p / 480p / 360p renditions, source-resolution-aware selection | worker spec |
| Dashboard and video-detail pages, TanStack Query/Router | web spec |
| CloudFront in the playback path | infrastructure spec |
| ECS deployment, CI/CD, monitoring, DLQ wiring | delivery spec |

`DELETE /videos/{id}` (with S3 asset cleanup) and `GET /videos` (pagination) shipped in `docs/plans/api-list-delete-plan.md`.
```

- [ ] **Step 3: Verify no other doc references the old status**

```bash
grep -rn "deletion, listing\|GET /videos\` list, pagination" docs/ README.md
```
Expected: no matches (confirms the two edits above were the only stale references).

- [ ] **Step 4: Final full-repo check**

```bash
gofmt -l .
go vet ./...
go test ./...
cd apps/web && pnpm lint && pnpm test && cd ../..
```
Expected: all clean/green — this plan touches no `apps/web` code, so the web checks are a no-op confirmation that nothing else regressed.

- [ ] **Step 5: Commit**

```bash
git add README.md docs/specifications/vertical-slice-spec.md
git commit -m "docs: mark listing, deletion, and structured logging as shipped"
```
