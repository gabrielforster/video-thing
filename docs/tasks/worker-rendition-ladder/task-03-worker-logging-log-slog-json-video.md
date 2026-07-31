# Task 3: Worker logging on log/slog JSON with video_id on every line

> Task 3 of 7 in [`worker-rendition-ladder`](00-context.md). Read [`00-context.md`](00-context.md) first — the goal, tech stack, Global Constraints, and file structure bind this task. Full plan: [`worker-rendition-ladder-plan.md`](../../plans/worker-rendition-ladder-plan.md).
>
> Previous: [Task 2](task-02-frame-rate-from-ffprobe-gop-length.md) · Next: [Task 4](task-04-per-rendition-transcode-arguments-multi-variant.md)

---

**Files:**
- Modify: `apps/worker/main.go`, `apps/worker/consumer.go`, `apps/worker/pipeline.go`
- Modify: `apps/worker/consumer_test.go` (the `captureLog` helper, plus one new test)
- Modify: `scripts/e2e.sh:177-179` (the receipt grep, which currently matches the old `log.Printf` text)

**Interfaces:**
- Consumes: `func (c *consumer) handle(ctx context.Context, msg types.Message)`, `func receiveCount(msg types.Message) int`, `func (p *pipeline) process(ctx context.Context, job uploadedObject) error`.
- Produces: no signature changes. A per-job logger `slog.With("video_id", job.VideoID.String())` inside both `handle` and `process`; `slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))` in `main`.

This lands before the ladder tasks so that Tasks 4-6 can log `upscaled_fallback`, the chosen renditions, and the thumbnail interval through the real logger instead of adding `log.Printf` lines that would immediately be rewritten. Per cross-plan contract 6 the handler is stdlib `log/slog` with a JSON handler — no logging dependency — and every worker line emitted while a job is in flight carries `video_id`. The video id is written with `.String()` rather than as a `uuid.UUID` value so the JSON is unambiguously the canonical text form that `scripts/e2e.sh` and the tests grep for.

- [ ] **Step 1: Write the failing test and replace the log-capture helper**

In `apps/worker/consumer_test.go`, replace the existing `captureLog` helper:

```go
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buf
}
```

and append this test:

```go
func TestEveryJobLogLineCarriesTheVideoID(t *testing.T) {
	logs := captureLog(t)
	c, _ := newTestConsumer(&fakeProcessor{}, &fakeWorkerStore{})

	c.handle(context.Background(), message("1"))

	output := strings.TrimSpace(logs.String())
	if output == "" {
		t.Fatal("handling a job logged nothing")
	}
	for _, line := range strings.Split(output, "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("log line is not JSON: %q: %v", line, err)
		}
		if entry["video_id"] != testVideoID {
			t.Errorf("log line %q has video_id %v, want %s", line, entry["video_id"], testVideoID)
		}
	}
}
```

The import block of `apps/worker/consumer_test.go` becomes:

```go
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/gabrielforster/video-thing/packages/database/db"
)
```

(`log` and `os` are no longer used by the helper.)

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./apps/worker -run TestEveryJobLogLineCarriesTheVideoID -v`
Expected: FAIL with `log line is not JSON: "processing video 3fa85f64-5717-4562-b3fc-2c963f66afa6 from video-thing-dev-raw-uploads/raw/3fa85f64-5717-4562-b3fc-2c963f66afa6"` — the consumer still writes free-text lines through the `log` package, and `captureLog` no longer intercepts them.

- [ ] **Step 3: Rewrite the logging in `apps/worker/consumer.go`**

Swap `"log"` for `"log/slog"` in the import block, then replace the three logging call sites.

In `run`, the receive-error branch:

```go
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			slog.Error("receive", "error", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(receiveBackoff):
			}
			continue
		}
```

All of `handle`:

```go
func (c *consumer) handle(ctx context.Context, msg types.Message) {
	job, err := parseUpload(aws.ToString(msg.Body))
	if err != nil {
		slog.Warn("discarding message", "error", err)
		c.delete(ctx, msg)
		return
	}

	logger := slog.With("video_id", job.VideoID.String())
	logger.Info("processing", "bucket", job.Bucket, "key", job.Key)

	if err := c.pipeline.process(ctx, job); err != nil {
		var perm *permanentError
		attempt := receiveCount(msg)

		if errors.As(err, &perm) || attempt >= maxAttempts {
			logger.Error("failed permanently", "attempt", attempt, "error", err)
			reason := err.Error()
			if _, dbErr := c.store.MarkFailed(ctx, db.MarkFailedParams{
				ID:           job.VideoID,
				ErrorMessage: &reason,
			}); dbErr != nil {
				if errors.Is(dbErr, pgx.ErrNoRows) {
					logger.Warn("no row to record the failure on, discarding the message")
					c.delete(ctx, msg)
					return
				}
				logger.Error("could not record failure", "error", dbErr)
				return
			}
			c.delete(ctx, msg)
			return
		}

		logger.Warn("failed, will retry", "attempt", attempt, "error", err)
		return
	}

	logger.Info("ready")
	c.delete(ctx, msg)
}
```

`receiveCount` and `delete`:

```go
func receiveCount(msg types.Message) int {
	raw := msg.Attributes[string(types.MessageSystemAttributeNameApproximateReceiveCount)]
	if raw == "" {
		slog.Warn("message has no ApproximateReceiveCount attribute; assuming attempt 1, "+
			"so the retry ceiling cannot engage", "max_attempts", maxAttempts)
		return 1
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		slog.Warn("unparseable ApproximateReceiveCount; assuming attempt 1, "+
			"so the retry ceiling cannot engage",
			"value", raw, "error", err, "max_attempts", maxAttempts)
		return 1
	}
	return n
}

func (c *consumer) delete(ctx context.Context, msg types.Message) {
	if _, err := c.sqs.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      aws.String(c.queueURL),
		ReceiptHandle: msg.ReceiptHandle,
	}); err != nil {
		slog.Error("delete message", "error", err)
	}
}
```

`receiveCount` and the two `parseUpload`/receive failures log without `video_id` on purpose: at those points there is no video id to attach. The existing tests that assert on the orphan-discard line (`TestFailureWriteForAMissingRowStillDeletesTheMessage`) and on the missing-attribute warning (`TestMissingReceiveCountIsLoggedLoudly`) still pass — the id is now the `video_id` field, and the warning text still contains `ApproximateReceiveCount`.

- [ ] **Step 4: Add the per-job logger to `apps/worker/pipeline.go`**

Add `"log/slog"` to the import block, and give `process` a logger plus two stage lines. The first three statements of `process` become:

```go
func (p *pipeline) process(ctx context.Context, job uploadedObject) error {
	logger := slog.With("video_id", job.VideoID.String())

	if _, err := p.store.MarkProcessing(ctx, job.VideoID); err != nil {
```

After the probe call, add:

```go
	logger.Info("probed",
		"width", probe.Width, "height", probe.Height,
		"duration_seconds", probe.Duration, "frame_rate", probe.FrameRate)
```

and immediately before `return nil` at the end of `process`, add:

```go
	logger.Info("uploaded", "master_playlist", playlistKey)
```

- [ ] **Step 5: Install the JSON handler in `apps/worker/main.go`**

Replace `"log"` with `"log/slog"` in the import block. Make the first statement of `main`:

```go
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
```

and replace every `log.Fatalf` with a `slog.Error` plus `os.Exit(1)`:

```go
	cfg, err := LoadConfig(os.Getenv)
	if err != nil {
		slog.Error("config", "error", err)
		os.Exit(1)
	}

	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			slog.Error("required binary not found in PATH", "binary", bin)
			os.Exit(1)
		}
	}
```

```go
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
```

and the tail of `main`:

```go
	slog.Info("worker polling", "queue_url", cfg.QueueURL)
	if err := c.run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("consumer", "error", err)
		os.Exit(1)
	}
```

`os.Exit` skips deferred calls, which is why `pool.Close` is registered only after the error checks that precede it — same shape the `log.Fatalf` version had.

- [ ] **Step 6: Fix the receipt grep in `scripts/e2e.sh`**

The delivery-diagnosis branch greps for the old free-text line. Replace lines 177-179:

```bash
    if grep -q "processing video $ID" "$TMP/worker.log" 2>/dev/null; then
        echo "FAIL: video $ID timed out with status=$STATUS, but the worker DID receive it" >&2
        echo "      (worker.log has a 'processing video $ID' line). Delivery worked and" >&2
```

with:

```bash
    if grep -q "\"video_id\":\"$ID\"" "$TMP/worker.log" 2>/dev/null; then
        echo "FAIL: video $ID timed out with status=$STATUS, but the worker DID receive it" >&2
        echo "      (worker.log has a JSON line with \"video_id\":\"$ID\"). Delivery worked and" >&2
```

- [ ] **Step 7: Run the tests**

Run: `go test ./apps/worker/... -v && gofmt -l . && go vet ./...`
Expected: PASS, including `TestEveryJobLogLineCarriesTheVideoID`, `TestMissingReceiveCountIsLoggedLoudly`, and `TestFailureWriteForAMissingRowStillDeletesTheMessage`. `gofmt -l` prints nothing.

- [ ] **Step 8: Commit**

```bash
git add apps/worker scripts/e2e.sh
git commit -m "refactor: log worker events as slog JSON with video_id on every line"
```

---
