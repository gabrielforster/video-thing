# Task 4: Per-rendition transcode arguments, multi-variant master playlist, and the full-ladder pipeline

> Task 4 of 7 in [`worker-rendition-ladder`](00-context.md). Read [`00-context.md`](00-context.md) first — the goal, tech stack, Global Constraints, and file structure bind this task. Full plan: [`worker-rendition-ladder-plan.md`](../../plans/worker-rendition-ladder-plan.md).
>
> Previous: [Task 3](task-03-worker-logging-log-slog-json-video.md) · Next: [Task 5](task-05-classify-ffmpeg-failures-fail-fast-retryable.md)

---

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
