# Task 6: Periodic scrub thumbnails

> Task 6 of 7 in [`worker-rendition-ladder`](00-context.md). Read [`00-context.md`](00-context.md) first — the goal, tech stack, Global Constraints, and file structure bind this task. Full plan: [`worker-rendition-ladder-plan.md`](../../plans/worker-rendition-ladder-plan.md).
>
> Previous: [Task 5](task-05-classify-ffmpeg-failures-fail-fast-retryable.md) · Next: [Task 7](task-07-end-to-end-ladder-proof-documentation.md)

---

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
