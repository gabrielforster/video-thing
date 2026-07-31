# Worker Rendition Ladder Implementation Plan — tasks

Split from [`worker-rendition-ladder-plan.md`](../../plans/worker-rendition-ladder-plan.md). Execute in order; each task ends
with a commit and is independently reviewable.

| # | Task | File |
|---|---|---|
| 1 | Rendition ladder and source-aware eligibility | [`task-01-rendition-ladder-source-aware-eligibility.md`](task-01-rendition-ladder-source-aware-eligibility.md) |
| 2 | Frame rate from ffprobe and GOP length that follows it | [`task-02-frame-rate-from-ffprobe-gop-length.md`](task-02-frame-rate-from-ffprobe-gop-length.md) |
| 3 | Worker logging on log/slog JSON with video_id on every line | [`task-03-worker-logging-log-slog-json-video.md`](task-03-worker-logging-log-slog-json-video.md) |
| 4 | Per-rendition transcode arguments, multi-variant master playlist, and the full-ladder pipeline | [`task-04-per-rendition-transcode-arguments-multi-variant.md`](task-04-per-rendition-transcode-arguments-multi-variant.md) |
| 5 | Classify ffmpeg failures into fail-fast and retryable | [`task-05-classify-ffmpeg-failures-fail-fast-retryable.md`](task-05-classify-ffmpeg-failures-fail-fast-retryable.md) |
| 6 | Periodic scrub thumbnails | [`task-06-periodic-scrub-thumbnails.md`](task-06-periodic-scrub-thumbnails.md) |
| 7 | End-to-end ladder proof and documentation | [`task-07-end-to-end-ladder-proof-documentation.md`](task-07-end-to-end-ladder-proof-documentation.md) |

---

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
