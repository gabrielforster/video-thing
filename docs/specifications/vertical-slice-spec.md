# Vertical Slice — Technical Specification

Status: Accepted
Date: 2026-07-26
Parent: [video-thing-spec.md](video-thing-spec.md)

## 1. Purpose

The MVP spec, the C4 model, the OpenAPI contract, the FFmpeg profiles, and the Terraform module tree are all written. No application code exists. This document specifies the first chunk of that code: a thin end-to-end slice that carries one real video through the entire pipeline — upload, event, transcode, playback — running locally against LocalStack and the compose Postgres.

The slice exists to prove the pipeline, not to complete it. Everything it defers is listed in [Section 3](#3-out-of-scope) with the spec that will own it.

## 2. Scope

A user opens the web page, picks a video file, and watches it become playable:

1. `POST /videos` creates a database record and returns a presigned S3 PUT URL.
2. The browser uploads the file directly to the raw bucket.
3. The S3 `ObjectCreated` notification lands on SQS.
4. The worker transcodes the file to a single 720p HLS rendition plus a cover thumbnail and uploads the result to the processed bucket.
5. The record flips to `ready` and the page plays the stream with hls.js.

## 3. Out of Scope

| Deferred | Owning spec |
|---|---|
| 1080p / 480p / 360p renditions, source-resolution-aware selection | worker spec |
| `DELETE /videos/{id}` and asset cleanup | api spec |
| `GET /videos` list, pagination | api spec |
| Dashboard and video-detail pages, TanStack Query/Router | web spec |
| CloudFront in the playback path | infrastructure spec |
| ECS deployment, CI/CD, monitoring, DLQ wiring | delivery spec |

Deferring CloudFront costs nothing at the code level: playback URLs are built from a single environment variable ([Section 9](#9-playback-urls)).

## 4. Repository Layout

```text
go.mod                      module github.com/gabrielforster/video-thing
apps/
    api/                    main.go, router.go, handlers.go, presign.go, config.go
    worker/                 main.go, consumer.go, pipeline.go, ffmpeg.go, config.go
    web/                    Vite + TypeScript + Tailwind + shadcn/ui
        src/                App.tsx, api.ts, components/
packages/
    database/
        migrations/         golang-migrate files
        queries.sql         sqlc source
        sqlc.yaml
        db/                 sqlc-generated code
scripts/
    e2e.sh
```

Three structural decisions:

**Single root `go.mod`.** Module path `github.com/gabrielforster/video-thing`. `apps/api` and `apps/worker` are `main` packages; `packages/database` is an ordinary internal package. One dependency tree, one `go test ./...`, no `go.work`. Per-module splitting buys independent versioning that two services sharing one database layer do not need.

**Migrations move to the code that owns them.** `docs/specifications/migrations/` becomes `packages/database/migrations/`. Those files are currently untracked, so nothing is lost. The reference in `database-schema.md` is updated to the new path.

**No `packages/contracts` or `packages/shared` yet.** API request and response types live in `apps/api`. The worker shares nothing with the API except the database layer. A contracts package with one consumer is an abstraction without a second implementation; it arrives when a second consumer does.

Data access is `sqlc` over `pgx/v5`, per [ADR-0001](../decisions/0001-go-for-backend-services.md). Schema changes run through the `golang-migrate` CLI from a Makefile target. Services never auto-migrate on boot, matching the CI/CD ordering in the [master spec, Section 18](video-thing-spec.md#18-cicd).

## 5. API Service

The UUID is generated in Go rather than by the `gen_random_uuid()` column default. The object key embeds the video ID, so the ID must exist before the row is inserted. The database default remains in place for any other writer.

Row on creation: `id`, `title`, `status = uploading`, `source_bucket = $RAW_BUCKET`, `source_key = raw/{id}`.

| Endpoint | Behavior |
|---|---|
| `POST /videos` | Insert the row, presign a `PUT` valid for 15 minutes, return the OpenAPI `CreateVideoResponse`. |
| `POST /videos/{id}/complete` | Transition `uploading` → `processing`. Any other current status returns `409 invalid_state_transition`. |
| `GET /videos/{id}` | Return the row, with `master_playlist` and `thumbnail` as absolute URLs. |
| `GET /healthz` | Static `200`. |
| `GET /readyz` | `200` when the database responds to a ping, `503` otherwise. |

Response shapes follow [openapi.yaml](openapi.yaml) exactly, including the `{ video, upload }` envelope on create and the `{ error: { code, message } }` envelope on failure.

Configuration is environment variables read at startup — no configuration library: `DATABASE_URL`, `RAW_BUCKET`, `AWS_ENDPOINT_URL` (set for LocalStack, empty in AWS), `PUBLIC_ASSET_BASE_URL`, `PORT`.

`POST /videos/{id}/complete` stays a UX optimization, exactly as the OpenAPI description states. It advances the status so the page can show progress immediately. It does not enqueue anything. If the browser never calls it, the S3 → SQS → worker path still processes the video and still corrects the status.

## 6. Worker Service

The worker long-polls SQS with a 20-second wait and handles one message at a time. Concurrency comes from running more tasks, not more goroutines inside a task — that is the model the queue-depth autoscaling in [master spec Section 14](video-thing-spec.md#14-ecs-worker-scaling) already assumes.

**Deriving the video ID.** An S3 event notification carries a bucket and a key, not a video ID. The key format `raw/{uuid}` established in [Section 5](#5-api-service) carries it: the worker takes the path segment after `raw/` and parses it as a UUID. A key that does not parse is poison — it is logged and deleted rather than retried forever.

The event body is decoded into a hand-written struct of about fifteen lines. Depending on `aws-lambda-go` for one type shape is not worth the module.

**Stages**, in order:

1. Set `status = processing`.
2. Download the original from the raw bucket to a temporary directory.
3. `ffprobe` for duration, width, height, and size.
4. Transcode to a single 720p HLS rendition, using the parameters in [ffmpeg-profiles.md](ffmpeg-profiles.md) §2 and §4.
5. Write `master.m3u8` referencing that one variant, per ffmpeg-profiles §5.3.
6. Extract `cover.jpg` at the 1-second mark.
7. Upload everything under `processed/{id}/`.
8. Set `status = ready` with `duration`, `width`, `height`, `size_bytes`, `master_playlist`, `thumbnail`.
9. Delete the SQS message.

The message is deleted only after the database write commits. A crash at any earlier point causes redelivery, which is what makes at-least-once delivery safe here. A redelivered job overwrites the same S3 keys and rewrites the same row, so re-running is idempotent.

**Failure handling.** A transient failure — S3 timeout, ffmpeg crash, database unavailable — is logged and the message is left undeleted, so the visibility timeout redelivers it. When `ApproximateReceiveCount` reaches 3, the worker stops retrying: it sets `status = failed` with `error_message` and deletes the message, so a permanently broken input cannot occupy the queue indefinitely. A failure that is a property of the input, per [ffmpeg-profiles.md §7](ffmpeg-profiles.md#7-failure-handling), short-circuits that ceiling and fails the record on the first attempt. An object at `raw/{uuid}` with no matching row is one of those: the row will never appear, and since there is nothing to record the failure on, the message is discarded rather than redelivered forever. The visibility timeout is 120 seconds — comfortably above one job, and low enough that the three-attempt ceiling is reached in minutes rather than the better part of an hour. DLQ wiring is deferred; this in-process ceiling covers the slice.

## 7. Web Application

One page, built with Vite, TypeScript, Tailwind, and shadcn/ui. No router and no data-fetching library — there is one route and one resource.

Flow: select a file → `POST /videos` → upload to the presigned URL → start polling `GET /videos/{id}` every 2 seconds → on `ready`, mount hls.js against `master_playlist`; on `failed`, render the error. `POST /videos/{id}/complete` is fired once polling has started and its failure is swallowed: the worker races that call and correctly answers `409` when it advanced the row first, and treating that as an upload error would strand the page and invite a duplicate upload of a video that is already processing.

The upload uses `XMLHttpRequest` rather than `fetch`, because `fetch` exposes no upload progress events and the page shows a progress bar. Polling is chosen over Server-Sent Events: a 2-second poll against a single record is a few lines and no server-side connection state. SSE arrives if the dashboard ever needs to watch many records at once.

## 8. Local Environment

`compose.yml` already provides LocalStack (S3 + SQS) and Postgres, and `docker/localstack-init/01-create-resources.sh` already creates both buckets, the queue, and the bucket notification wiring. The slice adds nothing to either.

`ffmpeg` and `ffprobe` are host binaries during development; the worker's container image is built in the delivery spec. A missing binary is detected at worker startup and fails fast rather than at the first message.

## 9. Playback URLs

Asset URLs are `${PUBLIC_ASSET_BASE_URL}/processed/{id}/master.m3u8` and `.../thumbnails/cover.jpg`.

Locally that base is `http://localhost:4566/video-thing-dev-processed-assets`. In AWS it is the CloudFront domain. The code does not branch on environment; only the variable changes.

## 10. Testing

**Go — tests written before the code they cover:** S3 event decoding and video-ID extraction, including the poison-key path; FFmpeg argument construction; `master.m3u8` assembly; and the status-transition rules behind the `409`. The sqlc query tests run against the compose Postgres and call `t.Skip` when `DATABASE_URL` is unset, so `go test ./...` stays green without Docker running.

**Web:** vitest with React Testing Library, with `XMLHttpRequest` and `fetch` mocked: the upload happy path, asserting the call order and that the player mounts once the polled status reaches `ready`; a rejected `POST /complete` still reaching `ready` and mounting the player, since that call is optional; a transient poll failure being retired by the next successful poll; and the `failed` status rendering its error state.

**End-to-end:** `scripts/e2e.sh` generates a 10-second `testsrc` clip with ffmpeg (long enough to produce more than one 6-second segment, and deliberately left at testsrc's native yuv444p so the worker's own pixel-format conforming is what is under test), drives the full pipeline against the local stack, and asserts that the record reached `ready`, that `processed/{id}/master.m3u8` and the rest of the asset set exist and are nonempty, that the API's `master_playlist` URL is exactly `PUBLIC_ASSET_BASE_URL` plus the key the worker wrote, that the same URL is readable unsigned and cross-origin the way hls.js reads it, and that `POST /videos/{id}/complete` returns `200` once and `409 invalid_state_transition` on a repeat.

## 11. Done When

* A file selected in the browser reaches the raw bucket without passing through the API.
* The S3 notification alone triggers processing — with `POST /complete` never called, the video still reaches `ready`.
* `processed/{id}/` contains `master.m3u8`, the 720p variant playlist, its segments, and `thumbnails/cover.jpg`.
* The page plays the result through hls.js.
* A worker killed mid-transcode reprocesses the video on redelivery and still reaches `ready`.
* An unprocessable input reaches `status = failed` with an `error_message`: on the first attempt when the failure is a property of the file (ffprobe rejects it, or an orphan object has no row), and after three attempts when the failure looked transient.
* `scripts/e2e.sh` passes from a cold `docker compose up`.
