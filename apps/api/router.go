package main

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
)

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

func newRouter(h *handlers, ping func(context.Context) error) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery(), gin.Logger(), cors())

	r.POST("/videos", h.createVideo)
	r.GET("/videos", h.listVideos)
	r.GET("/videos/:id", h.getVideo)
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
