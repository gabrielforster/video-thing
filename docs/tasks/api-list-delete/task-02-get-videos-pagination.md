# Task 2: `GET /videos` with pagination

> Task 2 of 7 in [`api-list-delete`](00-context.md). Read [`00-context.md`](00-context.md) first — the goal, tech stack, Global Constraints, and file structure bind this task. Full plan: [`api-list-delete-plan.md`](../../plans/api-list-delete-plan.md).
>
> Previous: [Task 1](task-01-sqlc-queries-listing-counting-deleting.md) · Next: [Task 3](task-03-s3-asset-deletion-s3assetcleaner-processed-bucket.md)

---

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
