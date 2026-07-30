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
