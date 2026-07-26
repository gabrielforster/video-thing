package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func (f *fakeStore) MarkProcessing(_ context.Context, id uuid.UUID) (db.Video, error) {
	v, ok := f.videos[id]
	if !ok {
		return db.Video{}, pgx.ErrNoRows
	}
	v.Status = db.VideoStatusProcessing
	f.videos[id] = v
	return v, nil
}

func testRouter(t *testing.T, s store) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := newHandlers(s, NewPresigner(testS3Client(t), "video-thing-dev-raw-uploads", 15*time.Minute),
		"video-thing-dev-raw-uploads", "http://localhost:4566/video-thing-dev-processed-assets")
	return newRouter(h, func(context.Context) error { return nil })
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

func TestHealthz(t *testing.T) {
	rec := do(t, testRouter(t, newFakeStore()), http.MethodGet, "/healthz", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}
