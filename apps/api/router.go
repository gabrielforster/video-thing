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
