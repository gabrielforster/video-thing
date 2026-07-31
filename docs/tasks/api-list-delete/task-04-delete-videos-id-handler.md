# Task 4: `DELETE /videos/{id}` handler

> Task 4 of 7 in [`api-list-delete`](00-context.md). Read [`00-context.md`](00-context.md) first — the goal, tech stack, Global Constraints, and file structure bind this task. Full plan: [`api-list-delete-plan.md`](../../plans/api-list-delete-plan.md).
>
> Previous: [Task 3](task-03-s3-asset-deletion-s3assetcleaner-processed-bucket.md) · Next: [Task 5](task-05-structured-json-logging-x-request-id.md)

---

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
