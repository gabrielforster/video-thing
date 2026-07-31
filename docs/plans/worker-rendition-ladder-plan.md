# Worker Rendition Ladder Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Take `apps/worker` from one hardcoded 720p rendition to the full four-rendition ladder specified in `docs/specifications/ffmpeg-profiles.md` — source-resolution-aware selection, correct per-job GOP alignment, a hand-assembled multi-variant master playlist, periodic scrub thumbnails, and ffmpeg failure classification.

**Architecture:** `ffmpeg.go` stays pure: a `rendition` value type, a descending `ladder` table holding §2's exact numbers, eligibility from the probed source height, per-rendition argument construction, and master-playlist assembly — all testable without running ffmpeg. `pipeline.go` sequences one `ffmpeg` process per eligible rendition (never `-var_stream_map`, per §5.3), fails the whole job if any rendition fails, and classifies ffmpeg stderr into fail-fast versus retryable. Worker logging becomes `log/slog` JSON so every line for a job carries its `video_id`.

**Tech Stack:** Go 1.25.5 (stdlib `log/slog`, `slices`, `cmp`, `math`), FFmpeg/ffprobe, AWS SDK for Go v2, bash + awscli for the end-to-end check.

**Depends on:** nothing — the vertical slice is enough. No database, API, or web change: per cross-plan contract 5 the ladder adds no columns, `master_playlist` stays the single master key, `thumbnail` stays the cover key, and `width`/`height` stay the *source* dimensions (confirmed in Task 4 — the stored dimensions describe the uploaded file, not the top rendition, and the API contract exposes them as such).

## Global Constraints

- Go 1.25.5, single root module `github.com/gabrielforster/video-thing`. No `go.work`, no per-app module.
- Every task ends gofmt-clean (`gofmt -l .` prints nothing), `go vet ./...` clean, `go test ./...` green.
- Response shapes match `openapi.yaml` exactly, including the `{error:{code,message}}` envelope. This plan does not touch the API.
- No new DB columns and no migration. `master_playlist` is the master key, `thumbnail` is the cover key, `width`/`height` are the source dimensions.
- Asset URL shape stays `${PUBLIC_ASSET_BASE_URL}/processed/{id}/master.m3u8` and `.../thumbnails/cover.jpg`. No code branches on environment.
- Every ladder number comes from `ffmpeg-profiles.md` §2; eligibility from §3; packaging from §4; command shapes from §5; thumbnails from §6; failure buckets from §7. Cite the section, do not restate it.
- Output key layout is fixed: `processed/{id}/master.m3u8`, `processed/{id}/{1080,720,480,360}/playlist.m3u8`, `.../segment_%05d.ts`, `processed/{id}/thumbnails/cover.jpg`, `processed/{id}/thumbnails/{second}.jpg`.
- Worker logging is `log/slog` with a JSON handler (stdlib — no logging dependency). Every line emitted while a job is in flight carries `video_id`.
- Comment discipline: this repo was deliberately stripped of explanatory comments. The only comment this plan adds or keeps is the `-pix_fmt yuv420p` block in `transcodeArgs`, which survives because §5.2 makes its absence a real bug. No doc comments on unexported identifiers, no comments in `_test.go` files.
- Tests before implementation. One commit per task minimum, conventional prefixes (`feat:` `fix:` `docs:` `test:` `refactor:` `chore:`). **Never add a `Co-Authored-By` trailer.**
- No new dependencies. Everything here is stdlib or already in `go.mod`.

## File Structure

| Path | Responsibility |
|---|---|
| `apps/worker/ffmpeg.go` | `rendition` type, the `ladder` table, eligibility, ffprobe parsing (incl. frame rate), GOP math, per-rendition transcode args, cover/scrub args, master playlist assembly |
| `apps/worker/ffmpeg_test.go` | Table tests for every pure function above, including §3's worked examples and §5.3's playlist byte-for-byte |
| `apps/worker/pipeline.go` | Per-rendition sequencing, ffmpeg failure classification, scrub-thumbnail placement, per-job `slog` logger |
| `apps/worker/pipeline_test.go` | Key layout, whole-job failure on a failed rendition, classifier, scrub placement |
| `apps/worker/consumer.go` | Long-poll loop and retry policy, now logging through `slog` |
| `apps/worker/consumer_test.go` | Existing retry/fail-fast tests plus "every job log line carries `video_id`" |
| `apps/worker/main.go` | Installs the JSON `slog` handler as the default logger |
| `scripts/e2e.sh` | Proves the ladder end to end: exact variant set, ascending order, per-variant segments, cover + scrub thumbnails |
| `README.md` | Status paragraph and the repository-layout worker line |
| `docs/specifications/vertical-slice-spec.md` | §3 deferred-work row for the ladder |
| `docs/specifications/ffmpeg-profiles.md` | §5.3's 360p `BANDWIDTH` arithmetic |

---

## Task 1: Rendition ladder and source-aware eligibility

**Files:**
- Modify: `apps/worker/ffmpeg.go` (append declarations; the existing single-rendition constants stay until Task 4)
- Test: `apps/worker/ffmpeg_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type rendition struct { Dir string; Width, Height int32; Profile, Level string; VideoKbps, MaxrateKbps, BufsizeKbps, AudioKbps int; Codecs string }`
  - `func (r rendition) bandwidth() int`
  - `var ladder []rendition` — the four §2 entries, descending
  - `func eligibleRenditions(sourceHeight int32) []rendition`
  - `func upscaledFallback(sourceHeight int32) bool`

The `ladder` slice is the single place the §2 table lives. `bandwidth()` is `(maxrate + audio) × 1000`, which is §4's peak-bandwidth rule for `EXT-X-STREAM-INF`. `eligibleRenditions` returns entries in ladder (descending) order; the master playlist re-sorts ascending in Task 4. `upscaledFallback` is a separate predicate so the caller can log §3's `upscaled_fallback=true` flag without the selector returning a tuple.

Note on §5.3's example block: its 480p/720p/1080p `BANDWIDTH` values match the stated `maxrate + audio` rule exactly (1500+96, 3000+128, 5350+128), but its 360p line reads `935000` where the rule gives `850000 + 96000 = 946000`. The rule and the other three lines win; the example has an arithmetic slip, which Task 7 corrects in the spec.

- [ ] **Step 1: Write the failing tests**

Append to `apps/worker/ffmpeg_test.go`:

```go
func TestLadderIsDescendingAndMatchesTheProfileTable(t *testing.T) {
	if len(ladder) != 4 {
		t.Fatalf("ladder has %d entries, want the 4 defined in ffmpeg-profiles §2", len(ladder))
	}
	for i, r := range ladder {
		if r.BufsizeKbps != 2*r.MaxrateKbps {
			t.Errorf("%s: bufsize = %dk, want 2x maxrate (%dk)", r.Dir, r.BufsizeKbps, 2*r.MaxrateKbps)
		}
		if r.MaxrateKbps <= r.VideoKbps {
			t.Errorf("%s: maxrate %dk must exceed the target %dk", r.Dir, r.MaxrateKbps, r.VideoKbps)
		}
		if i == 0 {
			continue
		}
		prev := ladder[i-1]
		if r.Height >= prev.Height {
			t.Errorf("ladder must be descending by height: %s follows %s", r.Dir, prev.Dir)
		}
		if r.bandwidth() >= prev.bandwidth() {
			t.Errorf("ladder must be descending by bandwidth: %s follows %s", r.Dir, prev.Dir)
		}
	}
}

func TestRenditionBandwidthIsPeakVideoPlusAudio(t *testing.T) {
	want := map[string]int{"1080": 5478000, "720": 3128000, "480": 1596000, "360": 946000}
	for _, r := range ladder {
		if got := r.bandwidth(); got != want[r.Dir] {
			t.Errorf("%s bandwidth = %d, want %d", r.Dir, got, want[r.Dir])
		}
	}
}

func TestEligibleRenditionsNeverUpscale(t *testing.T) {
	for _, tc := range []struct {
		name         string
		sourceHeight int32
		want         []string
		fallback     bool
	}{
		{"4k source", 2160, []string{"1080", "720", "480", "360"}, false},
		{"1080 source", 1080, []string{"1080", "720", "480", "360"}, false},
		{"720 source", 720, []string{"720", "480", "360"}, false},
		{"480 source", 480, []string{"480", "360"}, false},
		{"360 source", 360, []string{"360"}, false},
		{"240 source", 240, []string{"360"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := eligibleRenditions(tc.sourceHeight)
			dirs := make([]string, 0, len(got))
			for _, r := range got {
				dirs = append(dirs, r.Dir)
			}
			if !slices.Equal(dirs, tc.want) {
				t.Errorf("eligibleRenditions(%d) = %v, want %v", tc.sourceHeight, dirs, tc.want)
			}
			if got := upscaledFallback(tc.sourceHeight); got != tc.fallback {
				t.Errorf("upscaledFallback(%d) = %v, want %v", tc.sourceHeight, got, tc.fallback)
			}
		})
	}
}
```

`slices` is already imported by `ffmpeg_test.go`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./apps/worker -run 'TestLadder|TestRenditionBandwidth|TestEligibleRenditions' -v`
Expected: FAIL to build — `undefined: ladder`, `undefined: eligibleRenditions`, `undefined: upscaledFallback`.

- [ ] **Step 3: Append the ladder and the selector to `apps/worker/ffmpeg.go`**

Add at the end of the file (leave everything above it untouched — the single-rendition constants are removed in Task 4):

```go
type rendition struct {
	Dir         string
	Width       int32
	Height      int32
	Profile     string
	Level       string
	VideoKbps   int
	MaxrateKbps int
	BufsizeKbps int
	AudioKbps   int
	Codecs      string
}

func (r rendition) bandwidth() int {
	return (r.MaxrateKbps + r.AudioKbps) * 1000
}

var ladder = []rendition{
	{
		Dir: "1080", Width: 1920, Height: 1080,
		Profile: "high", Level: "4.1",
		VideoKbps: 5000, MaxrateKbps: 5350, BufsizeKbps: 10700, AudioKbps: 128,
		Codecs: "avc1.640029,mp4a.40.2",
	},
	{
		Dir: "720", Width: 1280, Height: 720,
		Profile: "main", Level: "3.1",
		VideoKbps: 2800, MaxrateKbps: 3000, BufsizeKbps: 6000, AudioKbps: 128,
		Codecs: "avc1.4d001f,mp4a.40.2",
	},
	{
		Dir: "480", Width: 854, Height: 480,
		Profile: "main", Level: "3.0",
		VideoKbps: 1400, MaxrateKbps: 1500, BufsizeKbps: 3000, AudioKbps: 96,
		Codecs: "avc1.4d001e,mp4a.40.2",
	},
	{
		Dir: "360", Width: 640, Height: 360,
		Profile: "baseline", Level: "3.0",
		VideoKbps: 800, MaxrateKbps: 850, BufsizeKbps: 1700, AudioKbps: 96,
		Codecs: "avc1.42001e,mp4a.40.2",
	},
}

func eligibleRenditions(sourceHeight int32) []rendition {
	var eligible []rendition
	for _, r := range ladder {
		if r.Height <= sourceHeight {
			eligible = append(eligible, r)
		}
	}
	if len(eligible) == 0 {
		return []rendition{ladder[len(ladder)-1]}
	}
	return eligible
}

func upscaledFallback(sourceHeight int32) bool {
	return sourceHeight < ladder[len(ladder)-1].Height
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./apps/worker/... && gofmt -l . && go vet ./...`
Expected: PASS, `gofmt -l` prints nothing, `go vet` silent.

- [ ] **Step 5: Commit**

```bash
git add apps/worker/ffmpeg.go apps/worker/ffmpeg_test.go
git commit -m "feat: add the four-rendition ladder and source-aware rendition selection"
```

---

## Task 2: Frame rate from ffprobe and GOP length that follows it

**Files:**
- Modify: `apps/worker/ffmpeg.go` (`probeResult`, `parseProbe`, new constants and helpers)
- Test: `apps/worker/ffmpeg_test.go`

**Interfaces:**
- Consumes: `parseProbe(stdout []byte) (probeResult, error)`.
- Produces:
  - `type probeResult struct { Width, Height int32; Duration, FrameRate float64 }`
  - `func parseFrameRate(raw string) (float64, error)`
  - `func effectiveFrameRate(fps float64) float64`
  - `func gopLength(fps float64) int`
  - `const maxFrameRate = 30`

**This task fixes a bug the vertical slice shipped.** Today `transcodeArgs` forces `-r 30` unconditionally and pins `keyint=180`. That is wrong twice over:

- §2's "Frame rate handling" column says frame rate is **passed through**, with a 30fps *ceiling* — `-r 30` belongs only on sources above 30fps. Re-timestamping a 24fps source to 30fps burns bitrate on duplicated frames for no quality gain (§2, notes).
- §4's closed-GOP row defines GOP length as **segment duration × frame rate**, and §5.2's note explains that the real invariant `keyint` protects is that GOP boundaries line up *across renditions of the same job*. A fixed 180 satisfies that only when the source is 30fps: against a 24fps source, 180 frames is 7.5 seconds of content inside 6-second segments, so keyframes and segment boundaries drift apart — exactly the failure §4 describes.

So `keyint`/`min-keyint` become `round(segmentSeconds × min(fps, 30))`, computed once per job from the probe and therefore identical for every rendition of that job, which is what §5.2's "`keyint`/`min-keyint` stay at 180 … so GOP boundaries line up across renditions" is actually protecting. `r_frame_rate` is a rational string (`30000/1001`), so it needs parsing rather than a float unmarshal.

An unparseable, missing, or non-positive frame rate is a property of the input, not of the environment, so it is an error — the pipeline already wraps probe errors in `permanent(...)`, which is §7's fail-fast bucket. Defaulting to 30 instead would silently produce misaligned GOPs.

- [ ] **Step 1: Write the failing tests**

Append to `apps/worker/ffmpeg_test.go`:

```go
func TestParseProbeReadsTheFrameRate(t *testing.T) {
	got, err := parseProbe([]byte(sampleProbe))
	if err != nil {
		t.Fatalf("parseProbe: %v", err)
	}
	if want := 30000.0 / 1001.0; got.FrameRate != want {
		t.Fatalf("frame rate = %v, want %v", got.FrameRate, want)
	}
}

func TestParseProbeRejectsAnUnusableFrameRate(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stdout string
	}{
		{"missing", `{"streams":[{"width":1920,"height":1080}],"format":{"duration":"10.0"}}`},
		{"zero denominator", `{"streams":[{"width":1920,"height":1080,"r_frame_rate":"30/0"}],"format":{"duration":"10.0"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseProbe([]byte(tc.stdout)); err == nil {
				t.Fatal("expected an error when the frame rate is unusable")
			}
		})
	}
}

func TestParseFrameRateHandlesRationals(t *testing.T) {
	for raw, want := range map[string]float64{
		"30000/1001": 30000.0 / 1001.0,
		"25/1":       25,
		"60/1":       60,
		"24000/1001": 24000.0 / 1001.0,
	} {
		got, err := parseFrameRate(raw)
		if err != nil {
			t.Fatalf("parseFrameRate(%q): %v", raw, err)
		}
		if got != want {
			t.Errorf("parseFrameRate(%q) = %v, want %v", raw, got, want)
		}
	}

	for _, raw := range []string{"", "30", "0/0", "30/0", "abc/1", "30/abc", "-30/1"} {
		if got, err := parseFrameRate(raw); err == nil {
			t.Errorf("parseFrameRate(%q) = %v, want an error", raw, got)
		}
	}
}

func TestEffectiveFrameRateCapsAtThirty(t *testing.T) {
	for fps, want := range map[float64]float64{60: 30, 50: 30, 30: 30, 25: 25, 24: 24} {
		if got := effectiveFrameRate(fps); got != want {
			t.Errorf("effectiveFrameRate(%v) = %v, want %v", fps, got, want)
		}
	}
}

func TestGopLengthIsSegmentDurationTimesCappedFrameRate(t *testing.T) {
	for fps, want := range map[float64]int{
		30000.0 / 1001.0: 180,
		30:               180,
		60:               180,
		50:               180,
		25:               150,
		24:               144,
		24000.0 / 1001.0: 144,
	} {
		if got := gopLength(fps); got != want {
			t.Errorf("gopLength(%v) = %d, want %d", fps, got, want)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./apps/worker -run 'TestParseProbeReads|TestParseProbeRejectsAnUnusable|TestParseFrameRate|TestEffectiveFrameRate|TestGopLength' -v`
Expected: FAIL to build — `undefined: parseFrameRate`, `undefined: effectiveFrameRate`, `undefined: gopLength`, and `got.FrameRate undefined (type probeResult has no field or method FrameRate)`.

- [ ] **Step 3: Edit `apps/worker/ffmpeg.go`**

The import block becomes:

```go
import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
)
```

Add `maxFrameRate` to the existing `const` block, next to `segmentSeconds`:

```go
	segmentSeconds     = 6
	maxFrameRate       = 30
)
```

Replace `probeResult` and `parseProbe` with:

```go
type probeResult struct {
	Width     int32
	Height    int32
	Duration  float64
	FrameRate float64
}

func parseProbe(stdout []byte) (probeResult, error) {
	var raw struct {
		Streams []struct {
			Width      int32  `json:"width"`
			Height     int32  `json:"height"`
			RFrameRate string `json:"r_frame_rate"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(stdout, &raw); err != nil {
		return probeResult{}, fmt.Errorf("parse ffprobe output: %w", err)
	}
	if len(raw.Streams) == 0 || raw.Streams[0].Height == 0 {
		return probeResult{}, errors.New("source has no video stream")
	}

	duration, err := strconv.ParseFloat(raw.Format.Duration, 64)
	if err != nil {
		return probeResult{}, fmt.Errorf("parse duration %q: %w", raw.Format.Duration, err)
	}

	frameRate, err := parseFrameRate(raw.Streams[0].RFrameRate)
	if err != nil {
		return probeResult{}, err
	}

	return probeResult{
		Width:     raw.Streams[0].Width,
		Height:    raw.Streams[0].Height,
		Duration:  duration,
		FrameRate: frameRate,
	}, nil
}

func parseFrameRate(raw string) (float64, error) {
	numerator, denominator, ok := strings.Cut(raw, "/")
	if !ok {
		return 0, fmt.Errorf("frame rate %q is not a rational", raw)
	}
	num, err := strconv.ParseFloat(numerator, 64)
	if err != nil {
		return 0, fmt.Errorf("frame rate numerator %q: %w", numerator, err)
	}
	den, err := strconv.ParseFloat(denominator, 64)
	if err != nil {
		return 0, fmt.Errorf("frame rate denominator %q: %w", denominator, err)
	}
	if num <= 0 || den <= 0 {
		return 0, fmt.Errorf("frame rate %q is not positive", raw)
	}
	return num / den, nil
}

func effectiveFrameRate(fps float64) float64 {
	return min(fps, float64(maxFrameRate))
}

func gopLength(fps float64) int {
	return int(math.Round(float64(segmentSeconds) * effectiveFrameRate(fps)))
}
```

`gopLength` and `effectiveFrameRate` have no callers until Task 4 — that is deliberate, so the frame-rate fix can be reviewed on its own.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./apps/worker/... && gofmt -l . && go vet ./...`
Expected: PASS (the pre-existing `TestParseProbe`, `TestParseProbeRejectsSourceWithoutVideoStream` and `TestParseProbeRejectsUnparseableDuration` still pass — `sampleProbe` already carries `r_frame_rate`, and the two rejection fixtures fail earlier, on the missing stream and the unparseable duration). `gofmt -l` prints nothing.

- [ ] **Step 5: Commit**

```bash
git add apps/worker/ffmpeg.go apps/worker/ffmpeg_test.go
git commit -m "fix: derive GOP length from the probed frame rate instead of assuming 30fps"
```

---

## Task 3: Worker logging on log/slog JSON with video_id on every line

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

## Task 4: Per-rendition transcode arguments, multi-variant master playlist, and the full-ladder pipeline

**Files:**
- Modify: `apps/worker/ffmpeg.go` (delete the single-rendition constants; rewrite `transcodeArgs` and `masterPlaylist`)
- Modify: `apps/worker/pipeline.go` (`transcodeAll`, and `process` loops the ladder)
- Test: `apps/worker/ffmpeg_test.go` (replace two tests, add four), `apps/worker/pipeline_test.go` (replace one test, add one)

**Interfaces:**
- Consumes: `ladder`, `eligibleRenditions`, `upscaledFallback`, `gopLength`, `maxFrameRate`, `segmentSeconds`, `probeResult.FrameRate`, and the per-job `logger` from Task 3.
- Produces:
  - `func transcodeArgs(src, outDir string, r rendition, fps float64) []string`
  - `func masterPlaylist(renditions []rendition) string`
  - `func kbps(v int) string`
  - `func transcodeAll(ctx context.Context, logger *slog.Logger, source, root string, renditions []rendition, fps float64) error`
- Removes: `renditionDir`, `renditionBandwidth`, `renditionCodecs`, `renditionWidth`, `renditionHeight`, `gopFrames`.

One `ffmpeg` process per eligible rendition, never `-var_stream_map` — §5.3 is explicit that the worker drives independent invocations and hand-assembles the master so per-rendition failures stay isolated and loggable. `masterPlaylist` sorts ascending by `bandwidth()` per §4's cold-start rule; `eligibleRenditions` hands it a descending slice, so the sort is what makes the file's order correct rather than incidental.

`-pix_fmt yuv420p` stays on **every** rendition and keeps its comment: §5.2 explains that the ladder's `-profile:v` values (baseline/main/high, all 8-bit 4:2:0) cannot represent a 4:2:2, 4:4:4, or 10-bit source and libx264 refuses the encode outright — ProRes, screen recorders, HDR phone captures, and ffmpeg's own `testsrc` (which `scripts/e2e.sh` deliberately feeds it) all fail without it. It is one of the few comments this repo keeps because its absence is a real bug, not a style preference.

`keyint`/`min-keyint` come from `gopLength(probe.FrameRate)` — one value per job, identical across every rendition of that job, which is the cross-rendition alignment §4 requires and §5.2's note is really protecting. `-r 30` is emitted only when the source exceeds the 30fps ceiling (§2).

**Stored dimensions:** `MarkReady` keeps `probe.Width`/`probe.Height`. Per cross-plan contract 5 there is no schema change and the columns describe the *source* file, not the top rendition — a 4K upload still reports 3840×2160 even though the highest rendition produced is 1080p. Confirmed; no further work.

- [ ] **Step 1: Replace the two obsolete ffmpeg tests and add the new ones**

In `apps/worker/ffmpeg_test.go`, delete `TestTranscodeArgsMatchProfile` and `TestMasterPlaylistListsTheOneRendition` entirely (both assert the single-rendition shape, including the unconditional `-r 30` that Task 2 established is wrong), and add:

```go
func renditionByDir(t *testing.T, dir string) rendition {
	t.Helper()
	for _, r := range ladder {
		if r.Dir == dir {
			return r
		}
	}
	t.Fatalf("no rendition %q in the ladder", dir)
	return rendition{}
}

func TestTranscodeArgsMatchTheProfileTablePerRendition(t *testing.T) {
	ntsc := 30000.0 / 1001.0

	for _, tc := range []struct {
		dir  string
		want map[string]string
	}{
		{"1080", map[string]string{
			"-vf":                   "scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2",
			"-profile:v":            "high",
			"-level:v":              "4.1",
			"-b:v":                  "5000k",
			"-maxrate":              "5350k",
			"-bufsize":              "10700k",
			"-b:a":                  "128k",
			"-hls_segment_filename": "/work/out/1080/segment_%05d.ts",
		}},
		{"720", map[string]string{
			"-vf":                   "scale=1280:720:force_original_aspect_ratio=decrease,pad=1280:720:(ow-iw)/2:(oh-ih)/2",
			"-profile:v":            "main",
			"-level:v":              "3.1",
			"-b:v":                  "2800k",
			"-maxrate":              "3000k",
			"-bufsize":              "6000k",
			"-b:a":                  "128k",
			"-hls_segment_filename": "/work/out/720/segment_%05d.ts",
		}},
		{"480", map[string]string{
			"-vf":                   "scale=854:480:force_original_aspect_ratio=decrease,pad=854:480:(ow-iw)/2:(oh-ih)/2",
			"-profile:v":            "main",
			"-level:v":              "3.0",
			"-b:v":                  "1400k",
			"-maxrate":              "1500k",
			"-bufsize":              "3000k",
			"-b:a":                  "96k",
			"-hls_segment_filename": "/work/out/480/segment_%05d.ts",
		}},
		{"360", map[string]string{
			"-vf":                   "scale=640:360:force_original_aspect_ratio=decrease,pad=640:360:(ow-iw)/2:(oh-ih)/2",
			"-profile:v":            "baseline",
			"-level:v":              "3.0",
			"-b:v":                  "800k",
			"-maxrate":              "850k",
			"-bufsize":              "1700k",
			"-b:a":                  "96k",
			"-hls_segment_filename": "/work/out/360/segment_%05d.ts",
		}},
	} {
		t.Run(tc.dir, func(t *testing.T) {
			args := transcodeArgs("/work/source.mp4", "/work/out", renditionByDir(t, tc.dir), ntsc)

			for flag, want := range tc.want {
				if got := argValue(t, args, flag); got != want {
					t.Errorf("%s = %q, want %q", flag, got, want)
				}
			}
			for flag, want := range map[string]string{
				"-i":                 "/work/source.mp4",
				"-c:v":               "libx264",
				"-pix_fmt":           "yuv420p",
				"-x264-params":       "keyint=180:min-keyint=180:scenecut=0:open-gop=0",
				"-c:a":               "aac",
				"-profile:a":         "aac_low",
				"-ar":                "48000",
				"-ac":                "2",
				"-f":                 "hls",
				"-hls_time":          "6",
				"-hls_playlist_type": "vod",
				"-hls_flags":         "independent_segments",
			} {
				if got := argValue(t, args, flag); got != want {
					t.Errorf("%s = %q, want %q", flag, got, want)
				}
			}

			if slices.Contains(args, "-r") {
				t.Errorf("a 29.97fps source must be passed through, not re-timestamped\ngot: %v", args)
			}
			if got := args[len(args)-1]; got != "/work/out/"+tc.dir+"/playlist.m3u8" {
				t.Errorf("output playlist = %q, want %q", got, "/work/out/"+tc.dir+"/playlist.m3u8")
			}
			if !slices.Contains(args, "-y") {
				t.Errorf("arg list is missing -y\ngot: %v", args)
			}
		})
	}
}

func TestTranscodeArgsForceThirtyOnlyAboveTheCeiling(t *testing.T) {
	r := renditionByDir(t, "720")

	fast := transcodeArgs("/work/source.mp4", "/work/out", r, 60)
	if got := argValue(t, fast, "-r"); got != "30" {
		t.Errorf("-r = %q for a 60fps source, want 30", got)
	}
	if got := argValue(t, fast, "-x264-params"); got != "keyint=180:min-keyint=180:scenecut=0:open-gop=0" {
		t.Errorf("-x264-params = %q, want the 6s x 30fps GOP", got)
	}

	slow := transcodeArgs("/work/source.mp4", "/work/out", r, 25)
	if slices.Contains(slow, "-r") {
		t.Errorf("a 25fps source must be passed through\ngot: %v", slow)
	}
	if got := argValue(t, slow, "-x264-params"); got != "keyint=150:min-keyint=150:scenecut=0:open-gop=0" {
		t.Errorf("-x264-params = %q, want the 6s x 25fps GOP", got)
	}
}

func TestGopIsIdenticalAcrossEveryRenditionOfAJob(t *testing.T) {
	var seen []string
	for _, r := range eligibleRenditions(1080) {
		seen = append(seen, argValue(t, transcodeArgs("/work/source.mp4", "/work/out", r, 24), "-x264-params"))
	}
	if len(seen) != 4 {
		t.Fatalf("expected 4 renditions for a 1080 source, got %d", len(seen))
	}
	for _, got := range seen {
		if got != seen[0] {
			t.Fatalf("GOP settings differ across renditions: %v", seen)
		}
	}
	if seen[0] != "keyint=144:min-keyint=144:scenecut=0:open-gop=0" {
		t.Errorf("-x264-params = %q, want the 6s x 24fps GOP", seen[0])
	}
}

func TestMasterPlaylistListsProducedRenditionsAscending(t *testing.T) {
	four := "#EXTM3U\n" +
		"#EXT-X-VERSION:3\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=946000,RESOLUTION=640x360,CODECS=\"avc1.42001e,mp4a.40.2\"\n" +
		"360/playlist.m3u8\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=1596000,RESOLUTION=854x480,CODECS=\"avc1.4d001e,mp4a.40.2\"\n" +
		"480/playlist.m3u8\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=3128000,RESOLUTION=1280x720,CODECS=\"avc1.4d001f,mp4a.40.2\"\n" +
		"720/playlist.m3u8\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=5478000,RESOLUTION=1920x1080,CODECS=\"avc1.640029,mp4a.40.2\"\n" +
		"1080/playlist.m3u8\n"
	if got := masterPlaylist(eligibleRenditions(1080)); got != four {
		t.Fatalf("1080 source master playlist =\n%q\nwant\n%q", got, four)
	}

	three := "#EXTM3U\n" +
		"#EXT-X-VERSION:3\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=946000,RESOLUTION=640x360,CODECS=\"avc1.42001e,mp4a.40.2\"\n" +
		"360/playlist.m3u8\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=1596000,RESOLUTION=854x480,CODECS=\"avc1.4d001e,mp4a.40.2\"\n" +
		"480/playlist.m3u8\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=3128000,RESOLUTION=1280x720,CODECS=\"avc1.4d001f,mp4a.40.2\"\n" +
		"720/playlist.m3u8\n"
	if got := masterPlaylist(eligibleRenditions(720)); got != three {
		t.Fatalf("720 source master playlist =\n%q\nwant\n%q", got, three)
	}
}
```

- [ ] **Step 2: Replace the pipeline key-layout test and add the whole-job failure test**

In `apps/worker/pipeline_test.go`, replace `TestRecordedKeysAreKeysThatGetUploaded` (it references the deleted `renditionDir`) with the version below, and append the new test:

```go
func TestRecordedKeysAreKeysThatGetUploaded(t *testing.T) {
	id := uuid.MustParse("3fa85f64-5717-4562-b3fc-2c963f66afa6")
	out := newOutputPaths(t.TempDir())

	paths := []string{out.playlist, out.cover}
	for _, r := range eligibleRenditions(1080) {
		paths = append(paths,
			filepath.Join(out.root, r.Dir, "playlist.m3u8"),
			filepath.Join(out.root, r.Dir, "segment_00000.ts"),
		)
	}

	for _, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir for %q: %v", path, err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %q: %v", path, err)
		}
	}

	uploaded := map[string]bool{}
	err := filepath.WalkDir(out.root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		key, err := objectKey(id, out.root, path)
		if err != nil {
			return err
		}
		uploaded[key] = true
		return nil
	})
	if err != nil {
		t.Fatalf("walking the output tree: %v", err)
	}

	playlistKey, coverKey, err := out.keys(id)
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	for _, key := range []string{playlistKey, coverKey} {
		if !uploaded[key] {
			t.Errorf("recorded key %q is not among the uploaded keys %v", key, uploaded)
		}
	}
	for _, dir := range []string{"1080", "720", "480", "360"} {
		key := "processed/" + id.String() + "/" + dir + "/segment_00000.ts"
		if !uploaded[key] {
			t.Errorf("segment key %q is not among the uploaded keys %v", key, uploaded)
		}
	}

	if want := "processed/" + id.String() + "/master.m3u8"; playlistKey != want {
		t.Errorf("playlist key = %q, want %q", playlistKey, want)
	}
	if want := "processed/" + id.String() + "/thumbnails/cover.jpg"; coverKey != want {
		t.Errorf("cover key = %q, want %q", coverKey, want)
	}
}

func TestTranscodeAllFailsTheWholeJobAtTheFirstBadRendition(t *testing.T) {
	root := t.TempDir()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	err := transcodeAll(context.Background(), logger,
		filepath.Join(root, "missing-source.mp4"), root, eligibleRenditions(1080), 30)
	if err == nil {
		t.Fatal("expected an error when the source file does not exist")
	}
	if !strings.Contains(err.Error(), "1080") {
		t.Errorf("the error must name the rendition that failed, got: %v", err)
	}
	for _, dir := range []string{"720", "480", "360"} {
		if _, statErr := os.Stat(filepath.Join(root, dir)); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("rendition %s was attempted after 1080 failed", dir)
		}
	}
}
```

The import block of `apps/worker/pipeline_test.go` becomes:

```go
import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./apps/worker -run 'TestTranscodeArgs|TestGopIsIdentical|TestMasterPlaylist|TestRecordedKeys|TestTranscodeAll' -v`
Expected: FAIL to build — `too many arguments in call to transcodeArgs`, `too many arguments in call to masterPlaylist`, and `undefined: transcodeAll`.

- [ ] **Step 4: Rewrite the constants, `transcodeArgs`, and `masterPlaylist` in `apps/worker/ffmpeg.go`**

The import block becomes:

```go
import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)
```

Replace the whole `const` block with:

```go
const (
	segmentSeconds = 6
	maxFrameRate   = 30
)
```

Replace `transcodeArgs` and `masterPlaylist` with:

```go
func kbps(v int) string {
	return strconv.Itoa(v) + "k"
}

func transcodeArgs(src, outDir string, r rendition, fps float64) []string {
	dir := filepath.Join(outDir, r.Dir)
	gop := gopLength(fps)

	args := []string{
		"-y", "-i", src,
		"-vf", fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2",
			r.Width, r.Height, r.Width, r.Height),
		"-c:v", "libx264",
		// yuv420p is mandatory, not a default: the ladder's -profile:v values
		// cannot encode a 4:2:2/4:4:4 or 10-bit source (ProRes, many screen
		// recorders, HDR phone captures, ffmpeg's own testsrc), and libx264
		// refuses the whole encode rather than converting on its own.
		"-pix_fmt", "yuv420p",
		"-profile:v", r.Profile, "-level:v", r.Level,
		"-b:v", kbps(r.VideoKbps), "-maxrate", kbps(r.MaxrateKbps), "-bufsize", kbps(r.BufsizeKbps),
	}

	if fps > float64(maxFrameRate) {
		args = append(args, "-r", strconv.Itoa(maxFrameRate))
	}

	return append(args,
		"-x264-params", fmt.Sprintf("keyint=%d:min-keyint=%d:scenecut=0:open-gop=0", gop, gop),
		"-c:a", "aac", "-profile:a", "aac_low", "-b:a", kbps(r.AudioKbps), "-ar", "48000", "-ac", "2",
		"-f", "hls",
		"-hls_time", strconv.Itoa(segmentSeconds),
		"-hls_playlist_type", "vod",
		"-hls_flags", "independent_segments",
		"-hls_segment_filename", filepath.Join(dir, "segment_%05d.ts"),
		filepath.Join(dir, "playlist.m3u8"),
	)
}

func masterPlaylist(renditions []rendition) string {
	ascending := slices.Clone(renditions)
	slices.SortFunc(ascending, func(a, b rendition) int {
		return cmp.Compare(a.bandwidth(), b.bandwidth())
	})

	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n")
	for _, r := range ascending {
		fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d,CODECS=%q\n%s/playlist.m3u8\n",
			r.bandwidth(), r.Width, r.Height, r.Codecs, r.Dir)
	}
	return b.String()
}
```

- [ ] **Step 5: Loop the ladder in `apps/worker/pipeline.go`**

Add `transcodeAll` above `process`:

```go
func transcodeAll(ctx context.Context, logger *slog.Logger, source, root string, renditions []rendition, fps float64) error {
	for _, r := range renditions {
		if err := os.MkdirAll(filepath.Join(root, r.Dir), 0o755); err != nil {
			return fmt.Errorf("output dir %s: %w", r.Dir, err)
		}
		if err := run(ctx, "ffmpeg", transcodeArgs(source, root, r, fps)); err != nil {
			return fmt.Errorf("transcode %s: %w", r.Dir, err)
		}
		logger.Info("rendition encoded", "rendition", r.Dir,
			"width", r.Width, "height", r.Height, "maxrate_kbps", r.MaxrateKbps)
	}
	return nil
}
```

Then, in `process`, replace everything from `out := newOutputPaths(filepath.Join(work, "out"))` through the closing brace of the `write master playlist` block (`apps/worker/pipeline.go:115-125` before this edit — the `newOutputPaths` line, the `MkdirAll` of the single `renditionDir`, the single `run(ctx, "ffmpeg", transcodeArgs(...))`, and the `os.WriteFile` of `masterPlaylist()`) with:

```go
	renditions := eligibleRenditions(probe.Height)
	dirs := make([]string, 0, len(renditions))
	for _, r := range renditions {
		dirs = append(dirs, r.Dir)
	}
	logger.Info("rendition ladder",
		"renditions", dirs,
		"upscaled_fallback", upscaledFallback(probe.Height))

	out := newOutputPaths(filepath.Join(work, "out"))
	if err := transcodeAll(ctx, logger, source, out.root, renditions, probe.FrameRate); err != nil {
		return err
	}

	if err := os.WriteFile(out.playlist, []byte(masterPlaylist(renditions)), 0o644); err != nil {
		return fmt.Errorf("write master playlist: %w", err)
	}
```

`transcodeAll` creates `out.root` on its way to the first rendition directory, so the `master.m3u8` write that follows it always has a directory to land in. Any rendition failing returns early: `MarkReady` is never called, the master playlist is never written, nothing is uploaded, and because the error is returned to `consumer.handle` the SQS message is not deleted — §7's third bucket, satisfied structurally rather than by a special case.

- [ ] **Step 6: Run the tests**

Run: `go test ./apps/worker/... -v && gofmt -l . && go vet ./...`
Expected: PASS, including the byte-for-byte four- and three-variant master playlists and `TestTranscodeAllFailsTheWholeJobAtTheFirstBadRendition`. `gofmt -l` prints nothing.

- [ ] **Step 7: Commit**

```bash
git add apps/worker
git commit -m "feat: encode every eligible rendition and assemble a multi-variant master playlist"
```

---

## Task 5: Classify ffmpeg failures into fail-fast and retryable

**Files:**
- Modify: `apps/worker/pipeline.go` (add `classifyFFmpeg`, use it in `transcodeAll` and the cover stage)
- Test: `apps/worker/pipeline_test.go`

**Interfaces:**
- Consumes: `func run(ctx context.Context, name string, args []string) error` (already puts ffmpeg's stderr in the error text), `func permanent(format string, args ...any) error`, `type permanentError struct{ err error }`.
- Produces:
  - `var fatalInputPatterns []string`
  - `func classifyFFmpeg(stage string, err error) error`

§7 defines three buckets. The third (a partial-rendition failure fails the whole video) is already structural after Task 4. This task implements the first two for `ffmpeg`:

- **Fail fast** on the known-fatal *input* patterns §7 names — `Invalid data found when processing input`, `moov atom not found`, `could not find codec parameters`. These are deterministic properties of the file, so `classifyFFmpeg` returns `permanent(...)` and `consumer.handle` writes `status=failed` and deletes the message on the first attempt instead of burning the whole redrive window rediscovering it.
- **Retryable** for everything else, which is exactly §7's environmental bucket — `Cannot allocate memory`, `No space left on device`, an OOM kill (`signal: killed`), a missing binary, an S3 error surfaced by the SDK. A plain error is returned, the message is left for redelivery, and no `status=failed` is written until the three-attempt ceiling.

Retryable is the default rather than a second pattern list: any stderr the worker has never seen before is far more likely to be an environment problem worth one retry than a poison pill worth failing instantly, and the `maxAttempts` ceiling plus the DLQ redrive policy are the backstop §7 assigns to that case.

Matching is case-insensitive because ffmpeg prints `Could not find codec parameters for stream 0 (...)` with a capital C while §7 writes the pattern lowercase; both forms must match, so the patterns are stored lowercase and the message is lowered before comparison.

`p.probe` is deliberately left alone: §7's first bucket says an `ffprobe` non-zero exit *is* the corrupt-input signal, so wrapping every probe failure in `permanent(...)` is already the specified behaviour.

- [ ] **Step 1: Write the failing tests**

Append to `apps/worker/pipeline_test.go`:

```go
func TestClassifyFFmpegFailsFastOnInputProperties(t *testing.T) {
	for _, stderr := range []string{
		"ffmpeg: exit status 1: /work/source: Invalid data found when processing input",
		"ffmpeg: exit status 1: [mov,mp4,m4a,3gp,3g2,mj2 @ 0x55] moov atom not found",
		"ffmpeg: exit status 1: Could not find codec parameters for stream 0 (Video: h264)",
		"ffmpeg: exit status 1: could not find codec parameters",
	} {
		err := classifyFFmpeg("transcode 720", errors.New(stderr))

		var perm *permanentError
		if !errors.As(err, &perm) {
			t.Errorf("%q must fail fast, got %v (%T)", stderr, err, err)
		}
		if !strings.Contains(err.Error(), "transcode 720") {
			t.Errorf("classified error must name the stage, got: %v", err)
		}
		if !strings.Contains(err.Error(), stderr) {
			t.Errorf("classified error must keep the ffmpeg output, got: %v", err)
		}
	}
}

func TestClassifyFFmpegKeepsEnvironmentalFailuresRetryable(t *testing.T) {
	for _, stderr := range []string{
		"ffmpeg: exit status 1: Cannot allocate memory",
		"ffmpeg: exit status 1: av_interleaved_write_frame(): No space left on device",
		"ffmpeg: signal: killed: ",
		"ffmpeg: exit status 1: /work/source: No such file or directory",
	} {
		err := classifyFFmpeg("transcode 1080", errors.New(stderr))

		var perm *permanentError
		if errors.As(err, &perm) {
			t.Errorf("%q is environmental and must stay retryable, got a permanentError", stderr)
		}
		if err == nil || !strings.Contains(err.Error(), "transcode 1080") {
			t.Errorf("classified error must name the stage, got: %v", err)
		}
	}
}

func TestClassifyFFmpegPassesSuccessThrough(t *testing.T) {
	if err := classifyFFmpeg("cover", nil); err != nil {
		t.Errorf("classifyFFmpeg(nil) = %v, want nil", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./apps/worker -run TestClassifyFFmpeg -v`
Expected: FAIL to build — `undefined: classifyFFmpeg`.

- [ ] **Step 3: Add the classifier to `apps/worker/pipeline.go`**

Add above `transcodeAll`:

```go
var fatalInputPatterns = []string{
	"invalid data found when processing input",
	"moov atom not found",
	"could not find codec parameters",
}

func classifyFFmpeg(stage string, err error) error {
	if err == nil {
		return nil
	}
	message := strings.ToLower(err.Error())
	for _, pattern := range fatalInputPatterns {
		if strings.Contains(message, pattern) {
			return permanent("%s: %v", stage, err)
		}
	}
	return fmt.Errorf("%s: %w", stage, err)
}
```

- [ ] **Step 4: Route the two ffmpeg stages through it**

In `transcodeAll`, replace the `run` error wrap:

```go
		if err := run(ctx, "ffmpeg", transcodeArgs(source, root, r, fps)); err != nil {
			return classifyFFmpeg("transcode "+r.Dir, err)
		}
```

In `process`, replace the cover error wrap:

```go
	if err := run(ctx, "ffmpeg", coverArgs(source, out.cover, probe.Duration)); err != nil {
		return classifyFFmpeg("cover", err)
	}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./apps/worker/... && gofmt -l . && go vet ./...`
Expected: PASS. `TestTranscodeAllFailsTheWholeJobAtTheFirstBadRendition` from Task 4 still passes — a missing source produces `No such file or directory`, which is not a fatal-input pattern, so it stays retryable while still naming `transcode 1080`.

- [ ] **Step 6: Commit**

```bash
git add apps/worker
git commit -m "feat: fail fast on unusable input and retry environmental ffmpeg failures"
```

---

## Task 6: Periodic scrub thumbnails

**Files:**
- Modify: `apps/worker/ffmpeg.go` (`scrubPlan`, `roundUpToTen`, `scrubArgs`)
- Modify: `apps/worker/pipeline.go` (`outputPaths.thumbnails`, `sequentialFrames`, `placeScrubFrames`, the thumbnail section of `process`)
- Test: `apps/worker/ffmpeg_test.go`, `apps/worker/pipeline_test.go`

**Interfaces:**
- Consumes: `func run(ctx context.Context, name string, args []string) error`, `func classifyFFmpeg(stage string, err error) error`, `probeResult.Duration`.
- Produces:
  - `const scrubIntervalSeconds = 10`, `const scrubLimit = 60`
  - `func scrubPlan(duration float64) (int, []int)`
  - `func roundUpToTen(seconds float64) int`
  - `func scrubArgs(src, pattern string, interval int) []string`
  - `func newOutputPaths(root string) outputPaths` gains a `thumbnails` field
  - `func sequentialFrames(dir string) ([]string, error)`
  - `func placeScrubFrames(seqDir, thumbDir string, offsets []int) error`

§6's second product does not exist today. It is one batch `ffmpeg` pass with `fps=1/N` — one decode instead of N seek-and-decode invocations — whose sequential output (`1.jpg, 2.jpg, …`) the worker renames to the true second offsets the key layout in §1 requires (`5.jpg, 15.jpg, …`), because `fps=1/10` samples from t=0 while the offsets are shifted by half an interval so the first frame is not a black slate.

Three concrete decisions:

- **The batch output goes to a scratch directory outside the upload tree** (`{work}/scrub`, not `{work}/out/...`). `uploadTree` walks `out.root`, so sequential frames written inside it would be uploaded under their meaningless numeric names, and renaming in place would collide — `2.jpg → 15.jpg` would clobber a not-yet-processed `15.jpg`. Renaming across directories removes both problems.
- **The cap is the hard invariant.** §6 caps thumbnails at 60 and widens the interval to `duration / 60` "rounded to the nearest 10s" rather than truncating coverage. Rounding to the *nearest* 10 can round down and yield 61-64 thumbnails (a 640s source: `640/60 = 10.67 → 10`, giving 64), which violates the bolded cap. `roundUpToTen` is the only rounding that satisfies both of §6's stated invariants at once, so the interval rounds up to the next 10s. For every duration in §6's spirit (10 minutes, an hour, ten hours) the two rules agree.
- **Only as many frames as offsets are placed.** If ffmpeg emits one extra frame at the tail (it does for durations that land exactly on an interval boundary), the surplus stays in the scratch directory, which is removed with the work directory and never reaches S3.

`-fps_mode vfr` replaces §6's literal `-vsync vfr`: `-vsync` is a deprecated alias, and the host ffmpeg (6.1.1) answers it with `-vsync is deprecated. Use -fps_mode` on every run. Same behaviour, no warning noise in `worker.log`, and it will not disappear from under the worker on the next ffmpeg major.

- [ ] **Step 1: Write the failing tests**

Append to `apps/worker/ffmpeg_test.go`:

```go
func TestScrubPlanCapsAtSixtyThumbnails(t *testing.T) {
	for _, tc := range []struct {
		name         string
		duration     float64
		wantInterval int
		wantCount    int
		wantFirst    int
		wantLast     int
	}{
		{"4s clip is shorter than the first offset", 4, 10, 0, 0, 0},
		{"8s clip still gets its t=5s frame", 8, 10, 1, 5, 5},
		{"30s clip", 30, 10, 3, 5, 25},
		{"10 minutes sits exactly on the cap", 600, 10, 60, 5, 595},
		{"1 hour widens the interval", 3600, 60, 60, 30, 3570},
		{"10 hours widens further", 36000, 600, 60, 300, 35700},
	} {
		t.Run(tc.name, func(t *testing.T) {
			interval, offsets := scrubPlan(tc.duration)
			if interval != tc.wantInterval {
				t.Errorf("interval = %d, want %d", interval, tc.wantInterval)
			}
			if len(offsets) != tc.wantCount {
				t.Fatalf("got %d offsets %v, want %d", len(offsets), offsets, tc.wantCount)
			}
			if tc.wantCount == 0 {
				return
			}
			if offsets[0] != tc.wantFirst {
				t.Errorf("first offset = %d, want %d", offsets[0], tc.wantFirst)
			}
			if last := offsets[len(offsets)-1]; last != tc.wantLast {
				t.Errorf("last offset = %d, want %d", last, tc.wantLast)
			}
			for i, offset := range offsets {
				if want := interval/2 + i*interval; offset != want {
					t.Fatalf("offset %d = %d, want %d", i, offset, want)
				}
				if float64(offset) >= tc.duration {
					t.Fatalf("offset %d is past the %.0fs source", offset, tc.duration)
				}
			}
		})
	}
}

func TestScrubPlanNeverExceedsTheCapJustAboveIt(t *testing.T) {
	for _, duration := range []float64{601, 640, 659, 1200} {
		interval, offsets := scrubPlan(duration)
		if len(offsets) > 60 {
			t.Errorf("duration %.0f produced %d offsets at interval %d, want at most 60",
				duration, len(offsets), interval)
		}
		if interval%10 != 0 {
			t.Errorf("interval %d for duration %.0f is not a multiple of 10s", interval, duration)
		}
	}
}

func TestScrubArgsAreASingleBatchPass(t *testing.T) {
	args := scrubArgs("/work/source.mp4", "/work/scrub/%d.jpg", 10)

	for flag, want := range map[string]string{
		"-i":        "/work/source.mp4",
		"-vf":       "fps=1/10,scale=320:-2",
		"-fps_mode": "vfr",
		"-q:v":      "4",
	} {
		if got := argValue(t, args, flag); got != want {
			t.Errorf("%s = %q, want %q", flag, got, want)
		}
	}
	if got := args[len(args)-1]; got != "/work/scrub/%d.jpg" {
		t.Errorf("output pattern = %q, want %q", got, "/work/scrub/%d.jpg")
	}
	if got := argValue(t, scrubArgs("/work/source.mp4", "/work/scrub/%d.jpg", 60), "-vf"); got != "fps=1/60,scale=320:-2" {
		t.Errorf("-vf = %q, want the widened interval", got)
	}
}
```

Append to `apps/worker/pipeline_test.go`:

```go
func TestPlaceScrubFramesRenamesInNumericOrderAndHonoursTheCap(t *testing.T) {
	seqDir := filepath.Join(t.TempDir(), "scrub")
	thumbDir := filepath.Join(t.TempDir(), "thumbnails")
	for _, dir := range []string{seqDir, thumbDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", dir, err)
		}
	}
	for _, name := range []string{"1.jpg", "2.jpg", "10.jpg", "11.jpg"} {
		if err := os.WriteFile(filepath.Join(seqDir, name), []byte(name), 0o644); err != nil {
			t.Fatalf("write %q: %v", name, err)
		}
	}

	if err := placeScrubFrames(seqDir, thumbDir, []int{5, 15, 25}); err != nil {
		t.Fatalf("placeScrubFrames: %v", err)
	}

	for name, want := range map[string]string{"5.jpg": "1.jpg", "15.jpg": "2.jpg", "25.jpg": "10.jpg"} {
		body, err := os.ReadFile(filepath.Join(thumbDir, name))
		if err != nil {
			t.Fatalf("read %q: %v", name, err)
		}
		if string(body) != want {
			t.Errorf("%s holds %q, want the frame from %q", name, body, want)
		}
	}

	placed, err := os.ReadDir(thumbDir)
	if err != nil {
		t.Fatalf("read thumbnail dir: %v", err)
	}
	if len(placed) != 3 {
		names := make([]string, 0, len(placed))
		for _, entry := range placed {
			names = append(names, entry.Name())
		}
		t.Errorf("thumbnail dir holds %v, want exactly the 3 offsets", names)
	}
}

func TestSequentialFramesSortNumerically(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"11.jpg", "2.jpg", "1.jpg", "10.jpg", "cover.jpg", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write %q: %v", name, err)
		}
	}

	got, err := sequentialFrames(dir)
	if err != nil {
		t.Fatalf("sequentialFrames: %v", err)
	}
	want := []string{"1.jpg", "2.jpg", "10.jpg", "11.jpg"}
	if len(got) != len(want) {
		t.Fatalf("sequentialFrames = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sequentialFrames = %v, want %v", got, want)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./apps/worker -run 'TestScrub|TestPlaceScrubFrames|TestSequentialFrames' -v`
Expected: FAIL to build — `undefined: scrubPlan`, `undefined: scrubArgs`, `undefined: placeScrubFrames`, `undefined: sequentialFrames`.

- [ ] **Step 3: Add the thumbnail planning to `apps/worker/ffmpeg.go`**

Append:

```go
const (
	scrubIntervalSeconds = 10
	scrubLimit           = 60
)

func roundUpToTen(seconds float64) int {
	interval := int(math.Ceil(seconds/10)) * 10
	if interval < scrubIntervalSeconds {
		interval = scrubIntervalSeconds
	}
	return interval
}

func scrubPlan(duration float64) (int, []int) {
	interval := scrubIntervalSeconds
	if duration/float64(interval) > float64(scrubLimit) {
		interval = roundUpToTen(duration / float64(scrubLimit))
	}

	var offsets []int
	for offset := interval / 2; float64(offset) < duration && len(offsets) < scrubLimit; offset += interval {
		offsets = append(offsets, offset)
	}
	return interval, offsets
}

func scrubArgs(src, pattern string, interval int) []string {
	return []string{
		"-y", "-i", src,
		"-vf", fmt.Sprintf("fps=1/%d,scale=320:-2", interval),
		"-fps_mode", "vfr",
		"-q:v", "4",
		pattern,
	}
}
```

- [ ] **Step 4: Place the frames from `apps/worker/pipeline.go`**

The import block becomes:

```go
import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/gabrielforster/video-thing/packages/database/db"
)
```

Give `outputPaths` a `thumbnails` field:

```go
type outputPaths struct {
	root       string
	playlist   string
	thumbnails string
	cover      string
}

func newOutputPaths(root string) outputPaths {
	thumbnails := filepath.Join(root, "thumbnails")
	return outputPaths{
		root:       root,
		playlist:   filepath.Join(root, "master.m3u8"),
		thumbnails: thumbnails,
		cover:      filepath.Join(thumbnails, "cover.jpg"),
	}
}
```

Add the two filesystem helpers below `contentTypeFor`:

```go
func sequentialFrames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	type frame struct {
		n    int
		name string
	}
	var frames []frame
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		stem, ok := strings.CutSuffix(entry.Name(), ".jpg")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(stem)
		if err != nil {
			continue
		}
		frames = append(frames, frame{n: n, name: entry.Name()})
	}
	slices.SortFunc(frames, func(a, b frame) int { return cmp.Compare(a.n, b.n) })

	names := make([]string, 0, len(frames))
	for _, f := range frames {
		names = append(names, f.name)
	}
	return names, nil
}

func placeScrubFrames(seqDir, thumbDir string, offsets []int) error {
	frames, err := sequentialFrames(seqDir)
	if err != nil {
		return err
	}
	for i, name := range frames {
		if i >= len(offsets) {
			break
		}
		dest := filepath.Join(thumbDir, strconv.Itoa(offsets[i])+".jpg")
		if err := os.Rename(filepath.Join(seqDir, name), dest); err != nil {
			return err
		}
	}
	return nil
}
```

Then replace the thumbnail section of `process` (the `thumbnail dir` mkdir and the cover `run`) with:

```go
	if err := os.MkdirAll(out.thumbnails, 0o755); err != nil {
		return fmt.Errorf("thumbnail dir: %w", err)
	}
	if err := run(ctx, "ffmpeg", coverArgs(source, out.cover, probe.Duration)); err != nil {
		return classifyFFmpeg("cover", err)
	}

	if interval, offsets := scrubPlan(probe.Duration); len(offsets) > 0 {
		seqDir := filepath.Join(work, "scrub")
		if err := os.MkdirAll(seqDir, 0o755); err != nil {
			return fmt.Errorf("scrub dir: %w", err)
		}
		if err := run(ctx, "ffmpeg", scrubArgs(source, filepath.Join(seqDir, "%d.jpg"), interval)); err != nil {
			return classifyFFmpeg("scrub thumbnails", err)
		}
		if err := placeScrubFrames(seqDir, out.thumbnails, offsets); err != nil {
			return fmt.Errorf("place scrub thumbnails: %w", err)
		}
		logger.Info("scrub thumbnails", "interval_seconds", interval, "count", len(offsets))
	}
```

`seqDir` is `{work}/scrub`, a sibling of `{work}/out` — deliberately outside the tree `uploadTree` walks.

- [ ] **Step 5: Run the tests**

Run: `go test ./apps/worker/... -v && gofmt -l . && go vet ./...`
Expected: PASS. `gofmt -l` prints nothing.

- [ ] **Step 6: Commit**

```bash
git add apps/worker
git commit -m "feat: generate periodic scrub thumbnails at true second offsets"
```

---

## Task 7: End-to-end ladder proof and documentation

**Files:**
- Modify: `scripts/e2e.sh:233-237` (the single-variant grep) and `scripts/e2e.sh:264-297` (the asset assertions and the PASS banner)
- Modify: `README.md` (status paragraph, repository-layout worker line)
- Modify: `docs/specifications/vertical-slice-spec.md` (§3 deferred row)
- Modify: `docs/specifications/ffmpeg-profiles.md` (§5.3's 360p `BANDWIDTH`)

**Interfaces:**
- Consumes: the running pipeline from Tasks 1-6 and the existing `e2e.sh` helpers `require_nonempty_object` and `queue_depth`, plus the variables `$ID`, `$TMP`, `$PROCESSED_BUCKET`, `$AWS_ENDPOINT_URL`, `$MASTER_LEN`, `$MASTER_URL`.
- Produces: no code interfaces. `scripts/e2e.sh` gains `$EXPECTED_VARIANTS`.

The script already generates a `1280x720` `testsrc` clip, which is exactly the interesting case: §3 makes 720/480/360 eligible and 1080 an upscale, so a passing run proves both the ladder *and* the no-upscale rule in one assertion. The four-variant playlist is pinned byte-for-byte by the unit test in Task 4; spending three extra 1080p encodes of e2e wall-clock to re-prove it buys nothing. The 10-second clip yields exactly one scrub frame (`fps=1/10` emits its first frame at t=0, renamed to `5.jpg`), so asserting the thumbnail set is exactly `cover.jpg` plus `5.jpg` proves the rename and the cap in one check.

- [ ] **Step 1: Replace the single-variant grep in `scripts/e2e.sh`**

Replace these five lines (currently 233-237):

```bash
if ! grep -q '720/playlist\.m3u8' "$TMP/master.m3u8"; then
    echo "FAIL: master playlist does not reference the 720p variant playlist ($MASTER_KEY):" >&2
    cat "$TMP/master.m3u8" >&2
    exit 1
fi
```

with:

```bash
EXPECTED_VARIANTS="360 480 720"

VARIANTS="$(grep -oE '^[0-9]+/playlist\.m3u8' "$TMP/master.m3u8" | cut -d/ -f1 | tr '\n' ' ' | sed 's/ *$//')"
if [ "$VARIANTS" != "$EXPECTED_VARIANTS" ]; then
    echo "FAIL: master playlist lists variants [$VARIANTS], want [$EXPECTED_VARIANTS] ($MASTER_KEY)" >&2
    echo "      the 1280x720 source makes 720/480/360 eligible; 1080 would be an upscale" >&2
    echo "      (ffmpeg-profiles.md section 3), and the order must ascend by bandwidth (section 4)" >&2
    cat "$TMP/master.m3u8" >&2
    exit 1
fi

PREV_BANDWIDTH=0
for bandwidth in $(grep -oE 'BANDWIDTH=[0-9]+' "$TMP/master.m3u8" | cut -d= -f2); do
    if [ "$bandwidth" -le "$PREV_BANDWIDTH" ]; then
        echo "FAIL: master playlist BANDWIDTH values are not ascending ($bandwidth after $PREV_BANDWIDTH):" >&2
        cat "$TMP/master.m3u8" >&2
        exit 1
    fi
    PREV_BANDWIDTH="$bandwidth"
done
```

- [ ] **Step 2: Replace the asset assertions and the PASS banner**

Replace everything from `COVER_LEN="$(require_nonempty_object ...` to the end of the file (currently 264-297) with:

```bash
COVER_LEN="$(require_nonempty_object "processed/$ID/thumbnails/cover.jpg" "cover thumbnail")"
SCRUB_LEN="$(require_nonempty_object "processed/$ID/thumbnails/5.jpg" "scrub thumbnail at t=5s")"

aws --endpoint-url "$AWS_ENDPOINT_URL" s3api list-objects-v2 \
    --bucket "$PROCESSED_BUCKET" --prefix "processed/$ID/thumbnails/" \
    --query 'Contents[].Key' --output text >"$TMP/thumbnails.txt"
THUMBS="$(tr '\t' '\n' <"$TMP/thumbnails.txt" \
    | sed "s|processed/$ID/thumbnails/||" | LC_ALL=C sort | tr '\n' ' ' | sed 's/ *$//')"
if [ "$THUMBS" != "5.jpg cover.jpg" ]; then
    echo "FAIL: thumbnails under processed/$ID/thumbnails/ are [$THUMBS], want [5.jpg cover.jpg]" >&2
    echo "      a 10s source yields exactly one scrub frame, at t=5s, and ffmpeg's sequential" >&2
    echo "      output must have been renamed to that true second offset" >&2
    exit 1
fi

TOTAL_SEGMENTS=0
for dir in $EXPECTED_VARIANTS; do
    PLAYLIST_LEN="$(require_nonempty_object "processed/$ID/$dir/playlist.m3u8" "${dir}p rendition playlist")"

    aws --endpoint-url "$AWS_ENDPOINT_URL" s3api list-objects-v2 \
        --bucket "$PROCESSED_BUCKET" --prefix "processed/$ID/$dir/" \
        --query 'Contents[].[Key,Size]' --output text >"$TMP/rendition-$dir.txt"

    SEGMENTS=0
    while IFS=$'\t' read -r key size; do
        [ -n "$key" ] || continue
        case "$key" in
            */segment_*.ts)
                if [ "${size:-0}" -le 0 ]; then
                    echo "FAIL: segment $key is zero-length" >&2
                    exit 1
                fi
                SEGMENTS=$((SEGMENTS + 1))
                ;;
        esac
    done <"$TMP/rendition-$dir.txt"

    if [ "$SEGMENTS" -lt 2 ]; then
        echo "FAIL: expected at least 2 segment_*.ts objects under processed/$ID/$dir/, found $SEGMENTS" >&2
        echo "objects actually present:" >&2
        cat "$TMP/rendition-$dir.txt" >&2
        exit 1
    fi

    echo "    ${dir}p: playlist ${PLAYLIST_LEN}B, $SEGMENTS nonempty segments"
    TOTAL_SEGMENTS=$((TOTAL_SEGMENTS + SEGMENTS))
done

echo "PASS: video $ID reached ready with a ${MASTER_LEN}B master playlist listing exactly"
echo "      [$VARIANTS] in ascending bandwidth (no 1080p from a 720p source), $TOTAL_SEGMENTS nonempty"
echo "      segments across the ladder, a ${COVER_LEN}B cover and a ${SCRUB_LEN}B scrub thumbnail;"
echo "      the API serves it as $MASTER_URL, readable unsigned and cross-origin"
```

- [ ] **Step 3: Run the end-to-end check from a cold stack**

Run: `make down && make e2e`
Expected: PASS, with the per-rendition lines printed for `360p`, `480p`, and `720p`. If a rendition times out, `worker.log` is dumped by the existing `cleanup` trap — the JSON lines from Task 3 (`"msg":"rendition ladder"`, `"msg":"rendition encoded"`) show how far the ladder got.

- [ ] **Step 4: Update `README.md`**

Replace the status paragraph (line 5) with:

```markdown
**Status:** vertical slice implemented, with the full rendition ladder. `apps/api`, `apps/worker`, and `apps/web` run locally against LocalStack (S3 + SQS) and Postgres: a browser can upload a file, the worker probes it and transcodes every rendition the source resolution supports (1080p/720p/480p/360p, never upscaling), assembles a `master.m3u8` ordered by ascending bandwidth, and writes a cover image plus a strip of scrub thumbnails; the page plays it back. `scripts/e2e.sh` proves the pipeline end to end from a cold stack. Deletion, listing, CloudFront, and deployment to AWS remain unbuilt — architecture, infrastructure, and API contract for that fuller scope are specified and the Terraform module tree is implemented and `terraform validate`-clean.
```

and the worker line in the repository-layout block (line 89):

```
    worker/             SQS consumer: ffmpeg transcode to the 1080p/720p/480p/360p HLS ladder,
                        thumbnails, DB updates
```

- [ ] **Step 5: Update the two specs**

In `docs/specifications/vertical-slice-spec.md` §3, replace the ladder row:

```markdown
| 1080p / 480p / 360p renditions, source-resolution-aware selection | worker spec |
```

with:

```markdown
| ~~1080p / 480p / 360p renditions, source-resolution-aware selection~~ | delivered by [worker-rendition-ladder-plan.md](../plans/worker-rendition-ladder-plan.md) |
```

In `docs/specifications/ffmpeg-profiles.md` §5.3, fix the 360p `BANDWIDTH` so the example agrees with the rule stated directly beneath it (`maxrate` 850k + audio 96k = 946000; the other three lines already compute this way):

```
#EXT-X-STREAM-INF:BANDWIDTH=946000,RESOLUTION=640x360,CODECS="avc1.42001e,mp4a.40.2"
```

- [ ] **Step 6: Verify the whole repository is clean**

Run: `gofmt -l . && go vet ./... && go test ./... && grep -rn "720p HLS" README.md docs/specifications/vertical-slice-spec.md`
Expected: no gofmt output, no vet output, all tests PASS, and the `grep` finds nothing (exit 1) — every "720p only" claim is gone.

- [ ] **Step 7: Commit**

```bash
git add scripts/e2e.sh README.md docs/specifications/vertical-slice-spec.md docs/specifications/ffmpeg-profiles.md
git commit -m "docs: prove the rendition ladder end to end and retire the 720p-only status"
```
