# Task 5: Classify ffmpeg failures into fail-fast and retryable

> Task 5 of 7 in [`worker-rendition-ladder`](00-context.md). Read [`00-context.md`](00-context.md) first — the goal, tech stack, Global Constraints, and file structure bind this task. Full plan: [`worker-rendition-ladder-plan.md`](../../plans/worker-rendition-ladder-plan.md).
>
> Previous: [Task 4](task-04-per-rendition-transcode-arguments-multi-variant.md) · Next: [Task 6](task-06-periodic-scrub-thumbnails.md)

---

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
