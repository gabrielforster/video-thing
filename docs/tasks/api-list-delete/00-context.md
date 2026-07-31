# API List/Delete/Logging Implementation Plan — tasks

Split from [`api-list-delete-plan.md`](../../plans/api-list-delete-plan.md). Execute in order; each task ends
with a commit and is independently reviewable.

| # | Task | File |
|---|---|---|
| 1 | sqlc queries for listing, counting, and deleting | [`task-01-sqlc-queries-listing-counting-deleting.md`](task-01-sqlc-queries-listing-counting-deleting.md) |
| 2 | `GET /videos` with pagination | [`task-02-get-videos-pagination.md`](task-02-get-videos-pagination.md) |
| 3 | S3 asset deletion (`S3AssetCleaner`) and `PROCESSED_BUCKET` config | [`task-03-s3-asset-deletion-s3assetcleaner-processed-bucket.md`](task-03-s3-asset-deletion-s3assetcleaner-processed-bucket.md) |
| 4 | `DELETE /videos/{id}` handler | [`task-04-delete-videos-id-handler.md`](task-04-delete-videos-id-handler.md) |
| 5 | Structured JSON logging and `X-Request-Id` middleware | [`task-05-structured-json-logging-x-request-id.md`](task-05-structured-json-logging-x-request-id.md) |
| 6 | Extend `scripts/e2e.sh` for listing, pagination, and deletion | [`task-06-extend-scripts-e2e-sh-listing-pagination.md`](task-06-extend-scripts-e2e-sh-listing-pagination.md) |
| 7 | Update docs invalidated by this plan | [`task-07-update-docs-invalidated-by-this-plan.md`](task-07-update-docs-invalidated-by-this-plan.md) |

---

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Finish the API surface `docs/specifications/openapi.yaml` already contracts — paginated `GET /videos` and `DELETE /videos/{id}` with S3 asset cleanup — and add the structured JSON logging `docs/specifications/video-thing-spec.md` §16 asks for.

**Architecture:** Three additive layers on top of the vertical slice, in dependency order: (1) three new sqlc queries (`ListVideos`, `CountVideos`, `DeleteVideo`) satisfied automatically by `pgStore`'s embedded `*db.Queries` — no `store.go` changes; (2) a narrow `s3API` interface (`ListObjectsV2` + `DeleteObjects`, mirroring the worker's `sqsAPI` pattern in `apps/worker/consumer.go`) behind a concrete `S3AssetCleaner`, so deletion logic is unit-testable without LocalStack; (3) a `log/slog` JSON handler plus an `X-Request-Id` gin middleware that replaces `gin.Logger()`. Response shapes are exactly what `openapi.yaml`'s `VideoList`/`PaginationMeta` schemas and cross-plan contract 1/3 specify, since the `web-dashboard` plan consumes them directly over HTTP.

**Tech Stack:** Go 1.25.5, Gin, pgx/v5, sqlc v1.31.1, AWS SDK for Go v2 (`s3` package), `log/slog` (stdlib, no new dependency), `github.com/google/uuid` (already a dependency) for request-id generation.

**Depends on:** nothing beyond the vertical slice (`docs/plans/vertical-slice-plan.md`) — that plan is finished and passing. `docs/plans/web-dashboard-plan.md` will call this plan's `GET /videos` and `DELETE /videos/{id}` endpoints, but does not need to land first; the response shapes below are fixed so that plan can be written against them now.

## Global Constraints

- Go 1.25.5, single root module `github.com/gabrielforster/video-thing`. No `go.work`, no per-app module.
- Every task ends `gofmt -l .` silent, `go vet ./...` clean, `go test ./...` green (DB-dependent tests skip when `DATABASE_URL` is unset, per the existing pattern in `packages/database/db/queries_test.go` and `apps/api/store_test.go`).
- Response shapes match `openapi.yaml` exactly, including the `{"error":{"code","message"}}` envelope. `GET /videos` returns `{"items":[...],"pagination":{"limit","offset","total","nextOffset"}}`; `items` is `[]`, never `null`, on an empty page.
- Generated code under `packages/database/db/` is produced only by `make sqlc` (`cd packages/database && sqlc generate`) and is never hand-edited.
- Services never auto-migrate on boot; this plan adds no migration (no new columns — contract 5).
- Structured logging is `log/slog` with `slog.NewJSONHandler`, stdlib only, no logging dependency.
- Commit convention: `feat:`, `fix:`, `docs:`, `chore:`, `refactor:`, `test:`. No `Co-Authored-By` trailer on any commit.
- Comment discipline: a comment only where its absence causes a real bug (e.g. `make([]videoJSON, ...)` vs. a nil slice serializing as `null`, or the deletion-ordering rationale). No doc comments on unexported identifiers, no comments in `_test.go` files.
- `s3.DeleteObjects` accepts at most 1000 keys per call; `S3AssetCleaner` must batch.
- Every task's Go code in this plan was compiled and its tests run (including live, against a real LocalStack + Postgres) while writing this plan, so the listed commands and expected outputs are not guesses.

## File Structure

| Path | Responsibility |
|---|---|
| `packages/database/queries.sql` | sqlc source; adds `ListVideos`, `CountVideos`, `DeleteVideo` |
| `packages/database/db/` | sqlc-generated code — regenerated via `make sqlc`, never hand-edited |
| `packages/database/db/queries_test.go` | Query-layer tests for the three new queries, skipped when `DATABASE_URL` is unset |
| `apps/api/handlers.go` | Adds pagination parsing + `listVideos` + `deleteVideo`; extends the `store` interface; adds the `assetCleaner` interface and `handlers.assets` field |
| `apps/api/handlers_test.go` | Fake store / asset-cleaner test doubles and handler tests for listing, deletion, and request-id behavior |
| `apps/api/router.go` | Adds `GET /videos` and `DELETE /videos/:id` routes; replaces `gin.Logger()` with a `slog`-based `requestLogging()` middleware |
| `apps/api/assets.go` | `s3API` interface (`ListObjectsV2` + `DeleteObjects`) and `S3AssetCleaner`, which deletes the raw key and paginates/batches deletion of everything under `processed/{id}/` |
| `apps/api/assets_test.go` | Fake-S3 tests: raw-key deletion, processed-prefix listing, 1000-key batching, list/delete error propagation |
| `apps/api/config.go` | Adds `ProcessedBucket` and its required-var check |
| `apps/api/config_test.go` | Updated for the new required variable |
| `apps/api/main.go` | Wires `S3AssetCleaner`, `cfg.ProcessedBucket`, and the JSON `slog` handler |
| `scripts/e2e.sh` | Adds `GET /videos` presence + pagination-validation + `DELETE`/404/bucket-cleanup assertions |
| `README.md` | Status paragraph and repository-layout line |
| `docs/specifications/vertical-slice-spec.md` | §3 deferred-work table |

---
