# Task 5: Structured JSON logging and `X-Request-Id` middleware

> Task 5 of 7 in [`api-list-delete`](00-context.md). Read [`00-context.md`](00-context.md) first — the goal, tech stack, Global Constraints, and file structure bind this task. Full plan: [`api-list-delete-plan.md`](../../plans/api-list-delete-plan.md).
>
> Previous: [Task 4](task-04-delete-videos-id-handler.md) · Next: [Task 6](task-06-extend-scripts-e2e-sh-listing-pagination.md)

---

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
