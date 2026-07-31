# Task 2: Frame rate from ffprobe and GOP length that follows it

> Task 2 of 7 in [`worker-rendition-ladder`](00-context.md). Read [`00-context.md`](00-context.md) first — the goal, tech stack, Global Constraints, and file structure bind this task. Full plan: [`worker-rendition-ladder-plan.md`](../../plans/worker-rendition-ladder-plan.md).
>
> Previous: [Task 1](task-01-rendition-ladder-source-aware-eligibility.md) · Next: [Task 3](task-03-worker-logging-log-slog-json-video.md)

---

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
