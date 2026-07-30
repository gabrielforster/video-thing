package main

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

type fakeStore struct {
	videos     map[uuid.UUID]db.Video
	createErr  error
	lastCreate db.CreateVideoParams
}

func newFakeStore() *fakeStore { return &fakeStore{videos: map[uuid.UUID]db.Video{}} }

func (f *fakeStore) CreateVideo(_ context.Context, arg db.CreateVideoParams) (db.Video, error) {
	if f.createErr != nil {
		return db.Video{}, f.createErr
	}
	f.lastCreate = arg
	now := pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
	v := db.Video{
		ID:           arg.ID,
		Title:        arg.Title,
		Status:       db.VideoStatusUploading,
		SourceBucket: arg.SourceBucket,
		SourceKey:    arg.SourceKey,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	f.videos[arg.ID] = v
	return v, nil
}

func (f *fakeStore) GetVideo(_ context.Context, id uuid.UUID) (db.Video, error) {
	v, ok := f.videos[id]
	if !ok {
		return db.Video{}, pgx.ErrNoRows
	}
	return v, nil
}

func (f *fakeStore) CompleteUpload(_ context.Context, id uuid.UUID) (db.Video, error) {
	v, ok := f.videos[id]
	if !ok {
		return db.Video{}, pgx.ErrNoRows
	}
	if v.Status != db.VideoStatusUploading {
		return db.Video{}, errNotUploading
	}
	v.Status = db.VideoStatusProcessing
	f.videos[id] = v
	return v, nil
}

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

func testRouter(t *testing.T, s store) *gin.Engine {
	t.Helper()
	return testRouterWithPing(t, s, func(context.Context) error { return nil })
}

func testRouterWithPing(t *testing.T, s store, ping func(context.Context) error) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := newHandlers(s, NewPresigner(testS3Client(t), "video-thing-dev-raw-uploads", 15*time.Minute),
		"video-thing-dev-raw-uploads", "http://localhost:4566/video-thing-dev-processed-assets")
	return newRouter(h, ping)
}

func do(t *testing.T, r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestCreateVideoReturnsUploadTarget(t *testing.T) {
	s := newFakeStore()
	rec := do(t, testRouter(t, s), http.MethodPost, "/videos", map[string]string{"title": "My Vacation Video"})

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	var got struct {
		Video struct {
			ID     string `json:"id"`
			Title  string `json:"title"`
			Status string `json:"status"`
		} `json:"video"`
		Upload struct {
			UploadURL string            `json:"uploadUrl"`
			Method    string            `json:"method"`
			ExpiresAt time.Time         `json:"expiresAt"`
			Headers   map[string]string `json:"headers"`
		} `json:"upload"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Video.Status != "uploading" {
		t.Fatalf("status = %q, want uploading", got.Video.Status)
	}
	if got.Upload.Method != "PUT" {
		t.Fatalf("method = %q, want PUT", got.Upload.Method)
	}
	if got.Upload.Headers["Content-Type"] != UploadContentType {
		t.Fatalf("Content-Type header = %q, want %q", got.Upload.Headers["Content-Type"], UploadContentType)
	}
	if want := "raw/" + got.Video.ID; s.lastCreate.SourceKey != want {
		t.Fatalf("source_key = %q, want %q", s.lastCreate.SourceKey, want)
	}
}

func TestCreateVideoRejectsEmptyTitle(t *testing.T) {
	rec := do(t, testRouter(t, newFakeStore()), http.MethodPost, "/videos", map[string]string{"title": ""})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var got struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Error.Code == "" {
		t.Fatal("error envelope missing code")
	}
}

func TestGetVideoBuildsAbsoluteAssetURLs(t *testing.T) {
	s := newFakeStore()
	id := uuid.New()
	playlist := "processed/" + id.String() + "/master.m3u8"
	thumb := "processed/" + id.String() + "/thumbnails/cover.jpg"
	duration, width, height, size := 12.5, int32(1280), int32(720), int64(4242)
	s.videos[id] = db.Video{
		ID: id, Title: "clip", Status: db.VideoStatusReady,
		Duration: &duration, Width: &width, Height: &height, SizeBytes: &size,
		MasterPlaylist: &playlist, Thumbnail: &thumb,
	}

	rec := do(t, testRouter(t, s), http.MethodGet, "/videos/"+id.String(), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var got struct {
		MasterPlaylist string `json:"master_playlist"`
		Thumbnail      string `json:"thumbnail"`
		Size           int64  `json:"size"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := "http://localhost:4566/video-thing-dev-processed-assets/" + playlist
	if got.MasterPlaylist != want {
		t.Fatalf("master_playlist = %q, want %q", got.MasterPlaylist, want)
	}
	if got.Size != size {
		t.Fatalf("size = %d, want %d", got.Size, size)
	}
}

func TestGetVideoNotFound(t *testing.T) {
	rec := do(t, testRouter(t, newFakeStore()), http.MethodGet, "/videos/"+uuid.New().String(), nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

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

func TestCompleteTransitionsUploadingToProcessing(t *testing.T) {
	s := newFakeStore()
	id := uuid.New()
	s.videos[id] = db.Video{ID: id, Title: "clip", Status: db.VideoStatusUploading}

	rec := do(t, testRouter(t, s), http.MethodPost, "/videos/"+id.String()+"/complete", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if s.videos[id].Status != db.VideoStatusProcessing {
		t.Fatalf("stored status = %q, want processing", s.videos[id].Status)
	}
}

func TestCompleteConflictsWhenNotUploading(t *testing.T) {
	s := newFakeStore()
	id := uuid.New()
	s.videos[id] = db.Video{ID: id, Title: "clip", Status: db.VideoStatusReady}

	rec := do(t, testRouter(t, s), http.MethodPost, "/videos/"+id.String()+"/complete", nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	var got struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Error.Code != "invalid_state_transition" {
		t.Fatalf("code = %q, want invalid_state_transition", got.Error.Code)
	}
}

func TestCompleteNotFound(t *testing.T) {
	rec := do(t, testRouter(t, newFakeStore()), http.MethodPost, "/videos/"+uuid.New().String()+"/complete", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHealthz(t *testing.T) {
	rec := do(t, testRouter(t, newFakeStore()), http.MethodGet, "/healthz", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

type readinessBody struct {
	Status string `json:"status"`
	Checks struct {
		Database string `json:"database"`
	} `json:"checks"`
}

func TestReadyzReportsDatabaseOK(t *testing.T) {
	r := testRouterWithPing(t, newFakeStore(), func(context.Context) error { return nil })
	rec := do(t, r, http.MethodGet, "/readyz", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var got readinessBody
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != "ok" {
		t.Fatalf("status = %q, want ok", got.Status)
	}
	if got.Checks.Database != "ok" {
		t.Fatalf("checks.database = %q, want ok", got.Checks.Database)
	}
}

func TestReadyzReportsDatabaseUnreachable(t *testing.T) {
	r := testRouterWithPing(t, newFakeStore(), func(context.Context) error { return errors.New("ping failed") })
	rec := do(t, r, http.MethodGet, "/readyz", nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", rec.Code, rec.Body.String())
	}
	var got readinessBody
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != "unavailable" {
		t.Fatalf("status = %q, want unavailable", got.Status)
	}
	if got.Checks.Database != "unreachable" {
		t.Fatalf("checks.database = %q, want unreachable", got.Checks.Database)
	}
}
