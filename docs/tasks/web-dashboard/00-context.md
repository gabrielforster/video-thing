# Web Dashboard and Video Pages Implementation Plan — tasks

Split from [`web-dashboard-plan.md`](../../plans/web-dashboard-plan.md). Execute in order; each task ends
with a commit and is independently reviewable.

| # | Task | File |
|---|---|---|
| 1 | TanStack Router + TanStack Query setup, root layout, retire the single page | [`task-01-tanstack-router-tanstack-query-setup-root.md`](task-01-tanstack-router-tanstack-query-setup-root.md) |
| 2 | API client — list and delete | [`task-02-api-client-list-delete.md`](task-02-api-client-list-delete.md) |
| 3 | Dashboard route — video list | [`task-03-dashboard-route-video-list.md`](task-03-dashboard-route-video-list.md) |
| 4 | Upload flow on the dashboard | [`task-04-upload-flow-dashboard.md`](task-04-upload-flow-dashboard.md) |
| 5 | Video route — player, metadata, delete | [`task-05-video-route-player-metadata-delete.md`](task-05-video-route-player-metadata-delete.md) |
| 6 | Documentation | [`task-06-documentation.md`](task-06-documentation.md) |

---

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the vertical slice's single upload page with the three-surface frontend `video-thing-spec.md` §13 specifies — a Dashboard listing every video, an Upload flow reachable from it, and a Video page with a player and metadata — built on TanStack Router and TanStack Query per §5's tech-stack table.

**Architecture:** `apps/web` gains a code-based route tree (`createRootRoute`/`createRoute`/`createRouter`, no file-based router plugin) with two leaf routes: `/` (Dashboard: video list + upload) and `/videos/$id` (player + metadata + delete). `apps/web/src/api.ts` gains `listVideos`/`deleteVideo` against the `api-list-delete` plan's endpoints. TanStack Query owns all server state — polling via `refetchInterval`, cache invalidation on upload success and delete — replacing the vertical slice's hand-rolled `setInterval` polling loop. Nothing on the Go side changes.

**Tech Stack:** React 19, Vite, TypeScript, TanStack Router (code-based routing), TanStack Query, Tailwind, shadcn/ui (`base-nova` style already configured), hls.js, vitest, @testing-library/react.

**Depends on:** `docs/plans/api-list-delete-plan.md`. This plan calls `GET /videos` and `DELETE /videos/{id}` as fixed contracts — do not run this plan's tasks against a build where those endpoints don't exist yet.

## Global Constraints

- Go 1.25.5, single root module `github.com/gabrielforster/video-thing`. No `go.work`, no per-app module. (Untouched by this plan — no Go files change.)
- Every task ends gofmt-clean, `go vet ./...` clean, `go test ./...` green. (Trivially true here since no `.go` file changes; re-run once at the end of Task 6 to prove it.)
- Web: pnpm only, never npm. `cd apps/web && pnpm test` and `cd apps/web && pnpm lint` green at every task boundary.
- Response shapes match `openapi.yaml` exactly, including the `{error:{code,message}}` envelope. This plan's client code never invents a shape `api-list-delete-plan.md` didn't define.
- Terraform: untouched by this plan.
- Secrets never land in the repo, in Terraform state committed to git, or in a Docker image. (Not applicable — no secrets in this plan.)
- Tests before implementation, one commit per task minimum.
- **This plan's own additions:**
  - Code-based route definitions only (`createRootRoute`/`createRoute`/`createRouter`) — no `@tanstack/router-plugin` / file-based routing. One fewer build-time moving part, and the route tree is small enough (2 leaf routes) that codegen buys nothing.
  - No new UI dependency beyond `@tanstack/react-router` and `@tanstack/react-query` (justified per-task, in Task 1). Confirmation dialogs use the native `window.confirm` rather than adding a shadcn dialog component — one boolean decision doesn't justify a new primitive.
  - Pagination beyond the API's default first page (`limit=20&offset=0`) is out of scope — the spec's Dashboard requirement (§13) is "list uploaded videos", not "paginate them"; a pager can be a later addition to `listVideos`, whose signature already takes `limit`/`offset`.
  - Query keys are fixed across this plan: `['videos']` for the list, `['video', id]` for a single video. Any task that invalidates the list uses the literal `['videos']` key.

## File Structure

| Path | Responsibility |
|---|---|
| `apps/web/src/router.tsx` | Route tree (`rootRoute`, `dashboardRoute`, `videoRoute`), `createAppRouter(history?)` factory, `Register` typing |
| `apps/web/src/router.test.tsx` | Task 1 only — proves a route mounts under `createMemoryHistory`; retired once Tasks 3 and 5 give the real pages their own tests |
| `apps/web/src/test-utils.tsx` | `renderRoute(path)` — mounts the real app router + a fresh `QueryClient` under memory history for tests |
| `apps/web/src/main.tsx` | Wires `QueryClientProvider` + `RouterProvider` for the real app |
| `apps/web/src/App.tsx`, `apps/web/src/App.test.tsx` | Deleted in Task 1 — see the retirement note there |
| `apps/web/src/api.ts` | Gains `listVideos`, `deleteVideo`, `Pagination`, `VideoList` |
| `apps/web/src/lib/format.ts` | `formatDuration` |
| `apps/web/src/lib/poll.ts` | `listRefetchInterval`, `videoRefetchInterval`, `isNotFoundError` — the pure decision logic behind every `refetchInterval` |
| `apps/web/src/routes/dashboard.tsx` | `/` — video grid + `UploadCard` |
| `apps/web/src/components/upload-card.tsx` | The upload form: create → upload → complete-nudge → navigate |
| `apps/web/src/routes/video.tsx` | `/videos/$id` — player, metadata, delete |
| `README.md`, `docs/specifications/vertical-slice-spec.md` | Status paragraph, repo-layout line, deferred-work table |

---
