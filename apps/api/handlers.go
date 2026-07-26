package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/gabrielforster/video-thing/packages/database/db"
)

type store interface {
	CreateVideo(ctx context.Context, arg db.CreateVideoParams) (db.Video, error)
	GetVideo(ctx context.Context, id uuid.UUID) (db.Video, error)
	CompleteUpload(ctx context.Context, id uuid.UUID) (db.Video, error)
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
