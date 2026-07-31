# Implementation plans

Each plan is executable on its own and ends with working, tested software. Every
task is a checkbox list of 2-5 minute steps with the complete code inline, meant to
be executed by an agent per `superpowers:subagent-driven-development`.

The plans are also split one-file-per-task under [`../tasks/`](../tasks/README.md).
Hand an implementer a single task file from there, never a whole plan.

| Plan | Delivers | Depends on |
|---|---|---|
| [vertical-slice-plan.md](vertical-slice-plan.md) | **Done.** One video end to end: presigned upload, S3 event, 720p HLS transcode, hls.js playback | — |
| [worker-rendition-ladder-plan.md](worker-rendition-ladder-plan.md) | The full 1080p/720p/480p/360p ladder, source-aware eligibility, scrub thumbnails, ffmpeg failure classification, worker `slog` | vertical slice |
| [api-list-delete-plan.md](api-list-delete-plan.md) | `GET /videos` with pagination, `DELETE /videos/{id}` with S3 cleanup, JSON logs + `X-Request-Id` | vertical slice |
| [web-dashboard-plan.md](web-dashboard-plan.md) | TanStack Router/Query, dashboard, video page, delete | api-list-delete |
| [delivery-plan.md](delivery-plan.md) | Container images, migrations as a gated ECS task, DLQ, one CloudFront distribution fronting `apps/web` + the API + playback, staging/production, OIDC CI/CD | nothing hard; the images are more useful once the two above land |

## Suggested order

`worker-rendition-ladder` and `api-list-delete` are independent — either order, or in
parallel. Then `web-dashboard` (it calls the new endpoints). `delivery` can start at
any point.

## Files two plans both touch

The plans were written to be independent, so a handful of files are edited by more
than one. Whichever lands second resolves the conflict; none is more than a few lines.

| File | Plans | What collides |
|---|---|---|
| `apps/worker/consumer.go` | worker-rendition-ladder (Task 3), delivery (Task 4) | the ladder plan moves logging to `slog`; the delivery plan raises `visibilityTimeoutSeconds` from 120 to 900 |
| `scripts/e2e.sh` | worker-rendition-ladder (Task 7), api-list-delete (Task 6) | both append assertions; the ladder plan also fixes a grep that matches the old free-text log line |
| `README.md`, `docs/specifications/vertical-slice-spec.md` §3 | all four | every plan's last task strikes its own row from the deferred table and updates the status paragraph |

## Settled by the owner

- `apps/web` is served from S3 behind CloudFront, on the same distribution that
  fronts the processed-assets bucket and the ALB — so the browser is same-origin with
  the API and no domain or ACM certificate is needed. See `delivery-plan.md`.
- Sizing is the floor everywhere: this is a demo, not a capacity plan. The worker's
  task size is the exception — it is the smallest that still completes the
  four-rendition ladder, not the smallest that exists.
- These plans are the only implementation document. The per-area specs
  `vertical-slice-spec.md` §3 promises (worker, api, web, delivery) will not be
  written; the master spec, `openapi.yaml` and `ffmpeg-profiles.md` already carry that
  design detail and the plans cite them by section.

## Accepted gaps

Known, deliberate, and not to be "fixed" inside a task without a decision:

- **No alarm notifications.** `sns_alarm_topic_arn` is empty in every environment, so
  every CloudWatch alarm the `monitoring` module creates changes state silently and
  notifies nobody. Deferred for a separate discussion — see `delivery-plan.md`.
- **CloudFront reaches the ALB over HTTP.** That hop is unencrypted inside AWS.
  Fixing it needs a domain and an ACM certificate.
- **No SQS visibility heartbeat.** A job outrunning the visibility timeout is
  redelivered while still running, and `MarkReady`/`MarkFailed` are unguarded on
  `status`, so the final row state depends on write order. The timeout is set wide
  enough that the ladder does not hit this on realistic sources.
- **No sweeper for orphaned assets.** Objects the worker writes after a `DELETE` has
  already cleaned up are never collected.

`delivery-plan.md` carries its own "Accepted gaps" section for the ones only that
plan can create — partial availability in `production`, no blue/green deploy, and
the CloudFront 403-only SPA fallback.

## Cross-plan contracts

These are fixed so the four plans agree. Do not renegotiate one inside a task.

1. `GET /videos` returns `{"items":[…],"pagination":{"limit","offset","total","nextOffset"}}`.
   `limit` default 20 / min 1 / max 100, `offset` default 0 / min 0, anything else is
   `400 invalid_request`. `nextOffset` is `offset+limit` while rows remain, else `null`.
   `items` is `[]`, never `null`.
2. New sqlc query names: `ListVideos`, `CountVideos`, `DeleteVideo` (the last one
   `RETURNING *`, so a 0-row delete is `pgx.ErrNoRows` → `404` and the returned row
   carries `source_bucket`/`source_key` for the cleanup).
3. `DELETE /videos/{id}` is `204` with no body, `404` when the row is gone, and deletes
   the row *before* the S3 objects — the ordering `sequence-diagrams.md` specifies.
4. Asset URLs stay `${PUBLIC_ASSET_BASE_URL}/processed/{id}/master.m3u8`. The database
   stores keys, the API prepends the base, and moving to CloudFront changes only that
   environment variable. No code branches on environment.
5. No new database columns. The ladder does not change the schema: `master_playlist`
   stays the single master key, `thumbnail` the cover key, `width`/`height` the source
   dimensions.
6. Structured logging is `log/slog` with a JSON handler — no logging dependency. API
   lines carry `request_id`, worker lines carry `video_id`.
