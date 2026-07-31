# Web Dashboard and Video Pages Implementation Plan

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

## Task 1: TanStack Router + TanStack Query setup, root layout, retire the single page

**Files:**
- Modify: `apps/web/package.json`, `apps/web/pnpm-lock.yaml` (add dependencies)
- Create: `apps/web/src/router.tsx`
- Create: `apps/web/src/router.test.tsx`
- Create: `apps/web/src/test-utils.tsx`
- Modify: `apps/web/src/main.tsx`
- Delete: `apps/web/src/App.tsx`, `apps/web/src/App.test.tsx`

**Interfaces:**
- Consumes: nothing new from this repo.
- Produces: `rootRoute`, `dashboardRoute`, `videoRoute`, `routeTree`, `createAppRouter(history?: RouterHistory)` from `src/router.tsx`; `renderRoute(initialPath: string)` from `src/test-utils.tsx`, returning `{ ...RenderResult, router, queryClient }`.

**What happens to `App.tsx` / `App.test.tsx` and its four cases:** both files are deleted in this task — `main.tsx` no longer has a single page to mount. Their four cases move forward like this:

1. *"uploads, then polls until the video is ready and shows the player"* splits in two: the ordering half (`createVideo` → `uploadFile` → `completeUpload` → navigate) becomes Task 4's `UploadCard` test "creates the video, uploads the file, nudges complete, and navigates to the video page"; the polling-to-ready-and-mount-player half becomes Task 5's video-route test "mounts the player once the video reaches ready" (now driven by `getVideo`, not by an upload).
2. *"still reaches ready when the optional complete call fails"* becomes Task 4's "still navigates to the video page when the optional complete call fails" — reaching `ready` is now the video route's job (covered by Task 5), so the upload flow only needs to prove a rejected nudge doesn't dead-end the navigation.
3. *"clears a transient poll error once a later poll succeeds"* moves verbatim to Task 5, now exercised against the video route's own `useQuery` instead of `App`'s `setInterval`.
4. *"shows the error state when processing fails"* moves verbatim to Task 5, same reasoning.

- [ ] **Step 1: Add the two dependencies**

```bash
cd apps/web
pnpm add @tanstack/react-router @tanstack/react-query
```

One line each, against §5's tech-stack table (`Frontend | React, Vite, TypeScript, TanStack Query/Router, Tailwind, shadcn/ui`):

- `@tanstack/react-router`: the spec mandates it, and the vertical slice deliberately deferred it (`vertical-slice-spec.md` §3: "Dashboard and video-detail pages, TanStack Query/Router"). It gives the app the two distinct routes §13 requires instead of one page with `if`-branches.
- `@tanstack/react-query`: same spec row, same deferral. It replaces the vertical slice's `useState`/`setInterval` polling loop with declarative caching, conditional polling (`refetchInterval`), and cache invalidation (`invalidateQueries`) on upload success and delete.

- [ ] **Step 2: Write the failing router test**

`apps/web/src/router.test.tsx`. Two placeholder route components prove the route tree resolves both paths and that `useParams` sees the `$id` segment, all under an in-memory history — the mechanism every later route test in this plan reuses via `renderRoute`.

```tsx
import { render, screen } from '@testing-library/react'
import { RouterProvider, createMemoryHistory } from '@tanstack/react-router'
import { describe, expect, it } from 'vitest'

import { createAppRouter } from './router'

describe('router', () => {
  it('mounts the dashboard route at /', () => {
    const router = createAppRouter(createMemoryHistory({ initialEntries: ['/'] }))
    render(<RouterProvider router={router} />)

    expect(screen.getByTestId('dashboard-placeholder')).toBeInTheDocument()
  })

  it('mounts the video route at /videos/:id with the id param available', () => {
    const router = createAppRouter(createMemoryHistory({ initialEntries: ['/videos/v1'] }))
    render(<RouterProvider router={router} />)

    expect(screen.getByTestId('video-placeholder')).toHaveTextContent('Video v1')
  })
})
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `cd apps/web && npx vitest run src/router.test.tsx`
Expected: FAIL — cannot resolve `./router`.

- [ ] **Step 4: Write `apps/web/src/router.tsx`**

```tsx
import {
  Outlet,
  createRootRoute,
  createRoute,
  createRouter,
  useParams,
  type RouterHistory,
} from '@tanstack/react-router'

export const rootRoute = createRootRoute({
  component: () => (
    <main className="mx-auto flex max-w-3xl flex-col gap-6 p-8">
      <h1 className="font-heading text-2xl font-semibold">Video Thing</h1>
      <Outlet />
    </main>
  ),
})

export const dashboardRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  component: () => <p data-testid="dashboard-placeholder">Dashboard</p>,
})

export const videoRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: 'videos/$id',
  component: function VideoPlaceholder() {
    const { id } = useParams({ from: '/videos/$id' })
    return <p data-testid="video-placeholder">Video {id}</p>
  },
})

export const routeTree = rootRoute.addChildren([dashboardRoute, videoRoute])

export function createAppRouter(history?: RouterHistory) {
  return createRouter({ routeTree, ...(history ? { history } : {}) })
}

declare module '@tanstack/react-router' {
  interface Register {
    router: ReturnType<typeof createAppRouter>
  }
}
```

If `createMemoryHistory` does not resolve from `@tanstack/react-router` in the installed version, import it from `@tanstack/history` instead (a transitive dependency already present) — the rest of this file is unaffected.

- [ ] **Step 5: Run the test to verify it passes**

Run: `cd apps/web && npx vitest run src/router.test.tsx`
Expected: both tests PASS.

- [ ] **Step 6: Write the shared test helper `apps/web/src/test-utils.tsx`**

Every later route test in this plan (Tasks 3, 4, 5) renders through this helper instead of mounting a component directly — it is the "exactly how" a route mounts under an in-memory history with the real query client wired in.

```tsx
import { render } from '@testing-library/react'
import { RouterProvider, createMemoryHistory } from '@tanstack/react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

import { createAppRouter } from './router'

export function renderRoute(initialPath: string) {
  const router = createAppRouter(createMemoryHistory({ initialEntries: [initialPath] }))
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })

  const result = render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )

  return { ...result, router, queryClient }
}
```

- [ ] **Step 7: Wire the real app in `apps/web/src/main.tsx`**

```tsx
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { RouterProvider } from '@tanstack/react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

import './index.css'
import { createAppRouter } from './router'

const queryClient = new QueryClient()
const router = createAppRouter()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </StrictMode>,
)
```

- [ ] **Step 8: Delete the retired single page**

```bash
rm apps/web/src/App.tsx apps/web/src/App.test.tsx
```

- [ ] **Step 9: Run the whole web suite and lint**

Run: `cd apps/web && pnpm test && pnpm lint`
Expected: all tests PASS (the router suite; `App.test.tsx` is gone so its four cases no longer run — see the retirement note above for where they land), lint clean.

- [ ] **Step 10: Commit**

```bash
git add apps/web/package.json apps/web/pnpm-lock.yaml apps/web/src/router.tsx apps/web/src/router.test.tsx apps/web/src/test-utils.tsx apps/web/src/main.tsx
git rm apps/web/src/App.tsx apps/web/src/App.test.tsx
git commit -m "feat: add TanStack Router/Query and retire the single-page app"
```

---

## Task 2: API client — list and delete

**Files:**
- Modify: `apps/web/src/api.ts`
- Modify: `apps/web/src/api.test.ts`

**Interfaces:**
- Consumes: `api-list-delete-plan.md`'s `GET /videos` and `DELETE /videos/{id}` (contracts 1 and 3 in the shared plan context — response shape fixed, not renegotiated here).
- Produces: `listVideos(limit?: number, offset?: number): Promise<VideoList>`, `deleteVideo(id: string): Promise<void>`, `Pagination`, `VideoList` types — consumed by Task 3 (`listVideos`) and Task 5 (`deleteVideo`).

- [ ] **Step 1: Write the failing tests**

Append to `apps/web/src/api.test.ts` (add `listVideos, deleteVideo` to the existing import line):

```ts
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { uploadFile, createVideo, listVideos, deleteVideo } from './api'
```

```ts
describe('listVideos', () => {
  it('requests the default page and returns items with pagination', async () => {
    const body = {
      items: [{ id: 'v1', status: 'ready' }],
      pagination: { limit: 20, offset: 0, total: 1, nextOffset: null },
    }
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, json: async () => body })
    vi.stubGlobal('fetch', fetchMock)

    const got = await listVideos()

    expect(got).toEqual(body)
    const [url] = fetchMock.mock.calls[0]
    expect(String(url)).toMatch(/\/videos\?limit=20&offset=0$/)
  })

  it('forwards a custom limit and offset', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ items: [], pagination: { limit: 5, offset: 10, total: 0, nextOffset: null } }),
    })
    vi.stubGlobal('fetch', fetchMock)

    await listVideos(5, 10)

    const [url] = fetchMock.mock.calls[0]
    expect(String(url)).toMatch(/\/videos\?limit=5&offset=10$/)
  })
})

describe('deleteVideo', () => {
  it('sends a DELETE request to the video', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 204 })
    vi.stubGlobal('fetch', fetchMock)

    await deleteVideo('v1')

    const [url, init] = fetchMock.mock.calls[0]
    expect(String(url)).toMatch(/\/videos\/v1$/)
    expect(init.method).toBe('DELETE')
  })

  it('throws on a 404', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: false,
      status: 404,
      text: async () => '{"error":{"code":"not_found","message":"Video not found."}}',
    })
    vi.stubGlobal('fetch', fetchMock)

    await expect(deleteVideo('missing')).rejects.toThrow(/404/)
  })
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd apps/web && npx vitest run src/api.test.ts`
Expected: FAIL — `listVideos`/`deleteVideo` are not exported by `./api`.

- [ ] **Step 3: Add to `apps/web/src/api.ts`**

Add these two interfaces after `CreateVideoResponse`:

```ts
export interface Pagination {
  limit: number
  offset: number
  total: number
  nextOffset: number | null
}

export interface VideoList {
  items: Video[]
  pagination: Pagination
}
```

Add these two functions after `getVideo`, before the `uploadFile` comment:

```ts
export async function listVideos(limit = 20, offset = 0): Promise<VideoList> {
  const params = new URLSearchParams({ limit: String(limit), offset: String(offset) })
  return json<VideoList>(await fetch(`${API_URL}/videos?${params}`))
}

export async function deleteVideo(id: string): Promise<void> {
  const response = await fetch(`${API_URL}/videos/${id}`, { method: 'DELETE' })
  if (response.ok) return
  const body = await response.text()
  throw new Error(`${response.status}: ${body}`)
}
```

`deleteVideo` cannot reuse the `json<T>` helper: a `204` has no body, and `json<T>` unconditionally calls `response.json()` on success.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd apps/web && npx vitest run src/api.test.ts`
Expected: all PASS (the original three plus these four).

- [ ] **Step 5: Run the whole web suite and lint**

Run: `cd apps/web && pnpm test && pnpm lint`
Expected: PASS, clean.

- [ ] **Step 6: Commit**

```bash
git add apps/web/src/api.ts apps/web/src/api.test.ts
git commit -m "feat: add listVideos and deleteVideo to the api client"
```

---

## Task 3: Dashboard route — video list

**Files:**
- Create: `apps/web/src/lib/format.ts`, `apps/web/src/lib/format.test.ts`
- Create: `apps/web/src/lib/poll.ts`, `apps/web/src/lib/poll.test.ts`
- Create: `apps/web/src/routes/dashboard.tsx`, `apps/web/src/routes/dashboard.test.tsx`
- Modify: `apps/web/src/router.tsx` (swap `dashboardRoute`'s placeholder for the real `Dashboard`)
- Modify: `apps/web/src/router.test.tsx` (drop the now-superseded dashboard-placeholder test)

**Interfaces:**
- Consumes: `listVideos` from Task 2; `renderRoute` from Task 1.
- Produces: `formatDuration(seconds: number | null): string`; `listRefetchInterval(items: Video[]): number | false`; `export default function Dashboard()` — consumed by Task 4, which adds the upload card to this same component.

- [ ] **Step 1: Write the failing `formatDuration` test**

`apps/web/src/lib/format.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { formatDuration } from './format'

describe('formatDuration', () => {
  it('formats whole minutes and seconds', () => {
    expect(formatDuration(65)).toBe('1:05')
  })

  it('rounds fractional seconds', () => {
    expect(formatDuration(125.6)).toBe('2:06')
  })

  it('pads single-digit seconds', () => {
    expect(formatDuration(9)).toBe('0:09')
  })

  it('returns a placeholder for null', () => {
    expect(formatDuration(null)).toBe('--:--')
  })
})
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd apps/web && npx vitest run src/lib/format.test.ts`
Expected: FAIL — cannot resolve `./format`.

- [ ] **Step 3: Write `apps/web/src/lib/format.ts`**

```ts
export function formatDuration(seconds: number | null): string {
  if (seconds === null || !Number.isFinite(seconds)) return '--:--'
  const total = Math.max(0, Math.round(seconds))
  const minutes = Math.floor(total / 60)
  const secs = total % 60
  return `${minutes}:${secs.toString().padStart(2, '0')}`
}
```

- [ ] **Step 4: Run it to verify it passes**

Run: `cd apps/web && npx vitest run src/lib/format.test.ts`
Expected: PASS.

- [ ] **Step 5: Write the failing `listRefetchInterval` test**

`apps/web/src/lib/poll.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { listRefetchInterval } from './poll'
import type { Video } from '@/api'

function video(overrides: Partial<Video>): Video {
  return {
    id: 'v1', title: 'clip', status: 'ready',
    duration: null, width: null, height: null, size: null,
    master_playlist: null, thumbnail: null,
    created_at: '', updated_at: '',
    ...overrides,
  }
}

describe('listRefetchInterval', () => {
  it('polls while a video is uploading', () => {
    expect(listRefetchInterval([video({ status: 'uploading' })])).toBe(2000)
  })

  it('polls while a video is processing', () => {
    expect(listRefetchInterval([video({ status: 'processing' })])).toBe(2000)
  })

  it('stops when every video is ready or failed', () => {
    expect(listRefetchInterval([video({ status: 'ready' }), video({ status: 'failed' })])).toBe(false)
  })

  it('stops on an empty list', () => {
    expect(listRefetchInterval([])).toBe(false)
  })
})
```

- [ ] **Step 6: Run it to verify it fails**

Run: `cd apps/web && npx vitest run src/lib/poll.test.ts`
Expected: FAIL — cannot resolve `./poll`.

- [ ] **Step 7: Write `apps/web/src/lib/poll.ts`**

```ts
import type { Video, VideoStatus } from '@/api'

function isActive(status: VideoStatus): boolean {
  return status === 'uploading' || status === 'processing'
}

export function listRefetchInterval(items: Video[]): number | false {
  return items.some((v) => isActive(v.status)) ? 2000 : false
}
```

- [ ] **Step 8: Run it to verify it passes**

Run: `cd apps/web && npx vitest run src/lib/poll.test.ts`
Expected: PASS.

- [ ] **Step 9: Write the failing dashboard test**

`apps/web/src/routes/dashboard.test.tsx`:

```tsx
import { screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi, afterEach } from 'vitest'

import { renderRoute } from '@/test-utils'
import * as api from '@/api'
import type { Video } from '@/api'

vi.mock('@/api', async () => {
  const actual = await vi.importActual<typeof api>('@/api')
  return { ...actual, listVideos: vi.fn() }
})

function video(overrides: Partial<Video>): Video {
  return {
    id: 'v1', title: 'clip', status: 'ready',
    duration: 65, width: 1280, height: 720, size: 100,
    master_playlist: null, thumbnail: null,
    created_at: '', updated_at: '',
    ...overrides,
  }
}

afterEach(() => vi.clearAllMocks())

describe('Dashboard', () => {
  it('renders a card per video with duration, resolution, and status', async () => {
    vi.mocked(api.listVideos).mockResolvedValue({
      items: [video({ id: 'v1', title: 'Clip One' })],
      pagination: { limit: 20, offset: 0, total: 1, nextOffset: null },
    })

    renderRoute('/')

    await waitFor(() => expect(screen.getByText('Clip One')).toBeInTheDocument())
    expect(screen.getByText(/1:05/)).toBeInTheDocument()
    expect(screen.getByText(/1280×720/)).toBeInTheDocument()
    expect(screen.getByText(/Status: ready/i)).toBeInTheDocument()
  })

  it('shows an empty state with no videos', async () => {
    vi.mocked(api.listVideos).mockResolvedValue({
      items: [],
      pagination: { limit: 20, offset: 0, total: 0, nextOffset: null },
    })

    renderRoute('/')

    await waitFor(() => expect(screen.getByText(/no videos yet/i)).toBeInTheDocument())
  })

  it('falls back to a placeholder when the thumbnail is missing', async () => {
    vi.mocked(api.listVideos).mockResolvedValue({
      items: [video({ thumbnail: null })],
      pagination: { limit: 20, offset: 0, total: 1, nextOffset: null },
    })

    renderRoute('/')

    await waitFor(() => expect(screen.getByText(/no preview/i)).toBeInTheDocument())
  })
})
```

- [ ] **Step 10: Run it to verify it fails**

Run: `cd apps/web && npx vitest run src/routes/dashboard.test.tsx`
Expected: FAIL — cannot resolve `../routes/dashboard` (or `@/router` still renders the placeholder, so `getByText('Clip One')` times out).

- [ ] **Step 11: Write `apps/web/src/routes/dashboard.tsx`**

```tsx
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'

import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { listVideos, type Video } from '@/api'
import { formatDuration } from '@/lib/format'
import { listRefetchInterval } from '@/lib/poll'

export default function Dashboard() {
  const query = useQuery({
    queryKey: ['videos'],
    queryFn: () => listVideos(),
    refetchInterval: (q) => listRefetchInterval(q.state.data?.items ?? []),
  })

  const items = query.data?.items ?? []

  return (
    <section className="flex flex-col gap-4">
      <h2 className="font-heading text-lg font-semibold">Videos</h2>
      {query.isSuccess && items.length === 0 && (
        <p className="text-sm text-muted-foreground">No videos yet. Upload one to get started.</p>
      )}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {items.map((video) => (
          <VideoCard key={video.id} video={video} />
        ))}
      </div>
    </section>
  )
}

function VideoCard({ video }: { video: Video }) {
  return (
    <Link to="/videos/$id" params={{ id: video.id }} className="block">
      <Card>
        <CardHeader>
          <CardTitle className="truncate">{video.title}</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-2">
          {video.thumbnail ? (
            <img src={video.thumbnail} alt="" className="aspect-video w-full rounded-md object-cover" />
          ) : (
            <div className="flex aspect-video w-full items-center justify-center rounded-md bg-muted text-xs text-muted-foreground">
              No preview
            </div>
          )}
          <p className="text-sm text-muted-foreground">Status: {video.status}</p>
          <p className="text-sm text-muted-foreground">
            {formatDuration(video.duration)} · {video.width && video.height ? `${video.width}×${video.height}` : '--'}
          </p>
        </CardContent>
      </Card>
    </Link>
  )
}
```

- [ ] **Step 12: Wire it into the route tree — modify `apps/web/src/router.tsx`**

```tsx
import {
  Outlet,
  createRootRoute,
  createRoute,
  createRouter,
  useParams,
  type RouterHistory,
} from '@tanstack/react-router'

import Dashboard from './routes/dashboard'

export const rootRoute = createRootRoute({
  component: () => (
    <main className="mx-auto flex max-w-3xl flex-col gap-6 p-8">
      <h1 className="font-heading text-2xl font-semibold">Video Thing</h1>
      <Outlet />
    </main>
  ),
})

export const dashboardRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  component: Dashboard,
})

export const videoRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: 'videos/$id',
  component: function VideoPlaceholder() {
    const { id } = useParams({ from: '/videos/$id' })
    return <p data-testid="video-placeholder">Video {id}</p>
  },
})

export const routeTree = rootRoute.addChildren([dashboardRoute, videoRoute])

export function createAppRouter(history?: RouterHistory) {
  return createRouter({ routeTree, ...(history ? { history } : {}) })
}

declare module '@tanstack/react-router' {
  interface Register {
    router: ReturnType<typeof createAppRouter>
  }
}
```

- [ ] **Step 13: Drop the superseded placeholder test — modify `apps/web/src/router.test.tsx`**

The dashboard route now renders the real `Dashboard`, so the placeholder assertion no longer applies; `dashboard.test.tsx` (Step 9) is its replacement. The video route is still a placeholder, so that half of this file stays until Task 5.

```tsx
import { render, screen } from '@testing-library/react'
import { RouterProvider, createMemoryHistory } from '@tanstack/react-router'
import { describe, expect, it } from 'vitest'

import { createAppRouter } from './router'

describe('router', () => {
  it('mounts the video route at /videos/:id with the id param available', () => {
    const router = createAppRouter(createMemoryHistory({ initialEntries: ['/videos/v1'] }))
    render(<RouterProvider router={router} />)

    expect(screen.getByTestId('video-placeholder')).toHaveTextContent('Video v1')
  })
})
```

- [ ] **Step 14: Run the whole web suite and lint**

Run: `cd apps/web && pnpm test && pnpm lint`
Expected: all PASS, clean.

- [ ] **Step 15: Commit**

```bash
git add apps/web/src/lib/format.ts apps/web/src/lib/format.test.ts apps/web/src/lib/poll.ts apps/web/src/lib/poll.test.ts apps/web/src/routes/dashboard.tsx apps/web/src/routes/dashboard.test.tsx apps/web/src/router.tsx apps/web/src/router.test.tsx
git commit -m "feat: add the dashboard video list"
```

---

## Task 4: Upload flow on the dashboard

**Files:**
- Create: `apps/web/src/components/upload-card.tsx`, `apps/web/src/components/upload-card.test.tsx`
- Modify: `apps/web/src/routes/dashboard.tsx` (render `UploadCard`)

**Interfaces:**
- Consumes: `createVideo`, `uploadFile`, `completeUpload` (unchanged from the vertical slice, `apps/web/src/api.ts`); `renderRoute` from Task 1.
- Produces: `export function UploadCard()` — consumed by `Dashboard`.

- [ ] **Step 1: Write the failing test**

`apps/web/src/components/upload-card.test.tsx`. Ports the vertical slice's ordering and rejected-complete cases (see the Task 1 retirement note); reaching `ready` and the failed-state rendering are now the video route's job, tested in Task 5.

```tsx
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'

import { renderRoute } from '@/test-utils'
import * as api from '@/api'

vi.mock('@/api', async () => {
  const actual = await vi.importActual<typeof api>('@/api')
  return { ...actual, createVideo: vi.fn(), uploadFile: vi.fn(), completeUpload: vi.fn(), listVideos: vi.fn() }
})

beforeEach(() => {
  vi.mocked(api.listVideos).mockResolvedValue({
    items: [],
    pagination: { limit: 20, offset: 0, total: 0, nextOffset: null },
  })
  vi.mocked(api.createVideo).mockResolvedValue({
    video: {
      id: 'v1', title: 'clip.mp4', status: 'uploading',
      duration: null, width: null, height: null, size: null,
      master_playlist: null, thumbnail: null, created_at: '', updated_at: '',
    },
    upload: { uploadUrl: 'http://s3.test/raw/v1', method: 'PUT', expiresAt: '', headers: {} },
  })
  vi.mocked(api.uploadFile).mockResolvedValue(undefined)
  vi.mocked(api.completeUpload).mockResolvedValue({
    id: 'v1', title: 'clip.mp4', status: 'processing',
    duration: null, width: null, height: null, size: null,
    master_playlist: null, thumbnail: null, created_at: '', updated_at: '',
  })
})
afterEach(() => vi.clearAllMocks())

async function selectAndUpload() {
  const file = new File(['bytes'], 'clip.mp4', { type: 'video/mp4' })
  await userEvent.upload(screen.getByLabelText(/video file/i), file)
  await userEvent.click(screen.getByRole('button', { name: /upload/i }))
}

describe('UploadCard', () => {
  it('creates the video, uploads the file, nudges complete, and navigates to the video page', async () => {
    const { router } = renderRoute('/')
    await selectAndUpload()

    await waitFor(() => expect(api.createVideo).toHaveBeenCalledWith('clip.mp4'))
    await waitFor(() => expect(api.uploadFile).toHaveBeenCalled())
    await waitFor(() => expect(api.completeUpload).toHaveBeenCalledWith('v1'))
    await waitFor(() => expect(router.state.location.pathname).toBe('/videos/v1'))
  })

  it('invalidates the video list once the upload completes', async () => {
    const { queryClient } = renderRoute('/')
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    await selectAndUpload()

    await waitFor(() => expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['videos'] }))
  })

  it('still navigates to the video page when the optional complete call fails', async () => {
    vi.mocked(api.completeUpload).mockRejectedValue(new Error('409: invalid_state_transition'))
    const { router } = renderRoute('/')

    await selectAndUpload()

    await waitFor(() => expect(router.state.location.pathname).toBe('/videos/v1'))
  })
})
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd apps/web && npx vitest run src/components/upload-card.test.tsx`
Expected: FAIL — cannot resolve `@/components/upload-card` (or `getByLabelText(/video file/i)` finds nothing, since `Dashboard` doesn't render an upload form yet).

- [ ] **Step 3: Write `apps/web/src/components/upload-card.tsx`**

```tsx
import { useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { useQueryClient } from '@tanstack/react-query'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Progress, ProgressLabel, ProgressValue } from '@/components/ui/progress'
import { completeUpload, createVideo, uploadFile } from '@/api'

type Phase = 'idle' | 'uploading'

export function UploadCard() {
  const [file, setFile] = useState<File | null>(null)
  const [progress, setProgress] = useState(0)
  const [phase, setPhase] = useState<Phase>('idle')
  const [error, setError] = useState<string | null>(null)
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  async function startUpload() {
    if (!file) return
    setError(null)
    setPhase('uploading')
    try {
      const created = await createVideo(file.name)
      await uploadFile(created.upload, file, setProgress)
      // The bytes are in S3, so the pipeline owns this video from here on.
      // The worker races this call and legitimately 409s when it already
      // advanced the row; treating that as an upload error would strand the
      // user on the dashboard instead of letting them watch it process.
      try {
        await completeUpload(created.video.id)
      } catch (err) {
        console.warn('complete upload nudge failed', err)
      }
      queryClient.invalidateQueries({ queryKey: ['videos'] })
      navigate({ to: '/videos/$id', params: { id: created.video.id } })
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setPhase('idle')
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Upload a video</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <label htmlFor="file" className="text-sm font-medium">Video file</label>
        <input
          id="file"
          type="file"
          accept="video/*"
          className="text-sm text-muted-foreground"
          onChange={(e) => setFile(e.target.files?.[0] ?? null)}
        />
        <Button className="self-start" disabled={!file || phase === 'uploading'} onClick={startUpload}>
          Upload
        </Button>

        {phase === 'uploading' && (
          <Progress value={progress}>
            <ProgressLabel>Uploading</ProgressLabel>
            <ProgressValue />
          </Progress>
        )}
        {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
      </CardContent>
    </Card>
  )
}
```

- [ ] **Step 4: Render it from the dashboard — modify `apps/web/src/routes/dashboard.tsx`**

```tsx
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'

import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { UploadCard } from '@/components/upload-card'
import { listVideos, type Video } from '@/api'
import { formatDuration } from '@/lib/format'
import { listRefetchInterval } from '@/lib/poll'

export default function Dashboard() {
  const query = useQuery({
    queryKey: ['videos'],
    queryFn: () => listVideos(),
    refetchInterval: (q) => listRefetchInterval(q.state.data?.items ?? []),
  })

  const items = query.data?.items ?? []

  return (
    <div className="flex flex-col gap-6">
      <UploadCard />

      <section className="flex flex-col gap-4">
        <h2 className="font-heading text-lg font-semibold">Videos</h2>
        {query.isSuccess && items.length === 0 && (
          <p className="text-sm text-muted-foreground">No videos yet. Upload one to get started.</p>
        )}
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {items.map((video) => (
            <VideoCard key={video.id} video={video} />
          ))}
        </div>
      </section>
    </div>
  )
}

function VideoCard({ video }: { video: Video }) {
  return (
    <Link to="/videos/$id" params={{ id: video.id }} className="block">
      <Card>
        <CardHeader>
          <CardTitle className="truncate">{video.title}</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-2">
          {video.thumbnail ? (
            <img src={video.thumbnail} alt="" className="aspect-video w-full rounded-md object-cover" />
          ) : (
            <div className="flex aspect-video w-full items-center justify-center rounded-md bg-muted text-xs text-muted-foreground">
              No preview
            </div>
          )}
          <p className="text-sm text-muted-foreground">Status: {video.status}</p>
          <p className="text-sm text-muted-foreground">
            {formatDuration(video.duration)} · {video.width && video.height ? `${video.width}×${video.height}` : '--'}
          </p>
        </CardContent>
      </Card>
    </Link>
  )
}
```

- [ ] **Step 5: Run the whole web suite and lint**

Run: `cd apps/web && pnpm test && pnpm lint`
Expected: all PASS, clean — including the three `dashboard.test.tsx` cases from Task 3, which don't interact with the upload form and are unaffected by it.

- [ ] **Step 6: Commit**

```bash
git add apps/web/src/components/upload-card.tsx apps/web/src/components/upload-card.test.tsx apps/web/src/routes/dashboard.tsx
git commit -m "feat: move the upload flow onto the dashboard"
```

---

## Task 5: Video route — player, metadata, delete

**Files:**
- Modify: `apps/web/src/lib/poll.ts`, `apps/web/src/lib/poll.test.ts` (add `videoRefetchInterval`, `isNotFoundError`)
- Create: `apps/web/src/routes/video.tsx`, `apps/web/src/routes/video.test.tsx`
- Modify: `apps/web/src/router.tsx` (swap `videoRoute`'s placeholder for the real `VideoPage`; add `pollMs` search validation)
- Delete: `apps/web/src/router.test.tsx` (its remaining case is superseded by `video.test.tsx`)

**Interfaces:**
- Consumes: `getVideo`, `deleteVideo` from `apps/web/src/api.ts`; `formatDuration` from Task 3; `renderRoute` from Task 1.
- Produces: `export default function VideoPage()`.

- [ ] **Step 1: Write the failing `poll.ts` additions test**

Append to `apps/web/src/lib/poll.test.ts`:

```ts
import { isNotFoundError, videoRefetchInterval } from './poll'
```

```ts
describe('videoRefetchInterval', () => {
  it('keeps polling while a video is uploading or processing', () => {
    expect(videoRefetchInterval(video({ status: 'processing' }), null, 2000)).toBe(2000)
  })

  it('stops once a video is ready', () => {
    expect(videoRefetchInterval(video({ status: 'ready' }), null, 2000)).toBe(false)
  })

  it('stops once a video has failed', () => {
    expect(videoRefetchInterval(video({ status: 'failed' }), null, 2000)).toBe(false)
  })

  it('stops on a 404', () => {
    expect(videoRefetchInterval(undefined, new Error('404: not found'), 2000)).toBe(false)
  })

  it('keeps polling through a transient error', () => {
    expect(videoRefetchInterval(undefined, new Error('network blip'), 2000)).toBe(2000)
  })
})

describe('isNotFoundError', () => {
  it('recognizes a 404 message', () => {
    expect(isNotFoundError(new Error('404: {"error":{"code":"not_found"}}'))).toBe(true)
  })

  it('rejects other errors', () => {
    expect(isNotFoundError(new Error('500: internal_error'))).toBe(false)
    expect(isNotFoundError(null)).toBe(false)
  })
})
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd apps/web && npx vitest run src/lib/poll.test.ts`
Expected: FAIL — `videoRefetchInterval`/`isNotFoundError` are not exported by `./poll`.

- [ ] **Step 3: Add to `apps/web/src/lib/poll.ts`**

```ts
export function isNotFoundError(error: unknown): boolean {
  return error instanceof Error && error.message.startsWith('404:')
}

export function videoRefetchInterval(video: Video | undefined, error: unknown, pollMs: number): number | false {
  if (isNotFoundError(error)) return false
  if (video && (video.status === 'ready' || video.status === 'failed')) return false
  return pollMs
}
```

- [ ] **Step 4: Run it to verify it passes**

Run: `cd apps/web && npx vitest run src/lib/poll.test.ts`
Expected: all PASS (4 from Task 3 plus 7 new).

- [ ] **Step 5: Write the failing video route test**

`apps/web/src/routes/video.test.tsx`. Ports the vertical slice's polling and failed-state cases verbatim (see the Task 1 retirement note), adds a not-found case for a `404`, and a delete-then-navigate case. Uses `?pollMs=10` in the initial path — the same trick the vertical slice used with an `App` prop — so the test doesn't wait on a real 2-second interval.

```tsx
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi, afterEach } from 'vitest'

import { renderRoute } from '@/test-utils'
import * as api from '@/api'
import type { Video } from '@/api'

vi.mock('@/api', async () => {
  const actual = await vi.importActual<typeof api>('@/api')
  return { ...actual, getVideo: vi.fn(), deleteVideo: vi.fn() }
})

function video(overrides: Partial<Video>): Video {
  return {
    id: 'v1', title: 'clip', status: 'processing',
    duration: 65, width: 1280, height: 720, size: 100,
    master_playlist: null, thumbnail: null,
    created_at: '', updated_at: '',
    ...overrides,
  }
}

afterEach(() => vi.clearAllMocks())

describe('VideoPage', () => {
  it('mounts the player once the video reaches ready', async () => {
    vi.mocked(api.getVideo)
      .mockResolvedValueOnce(video({ status: 'processing' }))
      .mockResolvedValue(video({ status: 'ready', master_playlist: 'http://cdn.test/processed/v1/master.m3u8' }))

    renderRoute('/videos/v1?pollMs=10')

    await waitFor(() => expect(screen.getByTestId('player')).toBeInTheDocument())
    expect(screen.getByTestId('player')).toHaveAttribute('data-src', 'http://cdn.test/processed/v1/master.m3u8')
  })

  it('clears a transient poll error once a later poll succeeds', async () => {
    vi.mocked(api.getVideo)
      .mockRejectedValueOnce(new Error('network blip'))
      .mockResolvedValue(video({ status: 'ready', master_playlist: 'http://cdn.test/processed/v1/master.m3u8' }))

    renderRoute('/videos/v1?pollMs=10')

    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent(/network blip/i))
    await waitFor(() => expect(screen.getByTestId('player')).toBeInTheDocument())
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('shows the error state when processing fails', async () => {
    vi.mocked(api.getVideo).mockResolvedValue(video({ status: 'failed' }))

    renderRoute('/videos/v1?pollMs=10')

    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent(/failed/i))
    expect(screen.queryByTestId('player')).not.toBeInTheDocument()
  })

  it('shows a not-found state on a 404', async () => {
    vi.mocked(api.getVideo).mockRejectedValue(
      new Error('404: {"error":{"code":"not_found","message":"Video not found."}}'),
    )

    renderRoute('/videos/missing')

    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent(/not found/i))
  })

  it('deletes the video, invalidates the list, and navigates home', async () => {
    vi.mocked(api.getVideo).mockResolvedValue(
      video({ status: 'ready', master_playlist: 'http://cdn.test/processed/v1/master.m3u8' }),
    )
    vi.mocked(api.deleteVideo).mockResolvedValue(undefined)
    vi.spyOn(window, 'confirm').mockReturnValue(true)

    const { router, queryClient } = renderRoute('/videos/v1?pollMs=10')
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    await waitFor(() => expect(screen.getByTestId('player')).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: /delete/i }))

    await waitFor(() => expect(api.deleteVideo).toHaveBeenCalledWith('v1'))
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['videos'] })
    await waitFor(() => expect(router.state.location.pathname).toBe('/'))
  })
})
```

- [ ] **Step 6: Run it to verify it fails**

Run: `cd apps/web && npx vitest run src/routes/video.test.tsx`
Expected: FAIL — cannot resolve `../routes/video` (or the placeholder route renders `Video v1` instead of a player/card).

- [ ] **Step 7: Write `apps/web/src/routes/video.tsx`**

```tsx
import { useEffect, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useParams, useSearch } from '@tanstack/react-router'
import Hls from 'hls.js'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { deleteVideo, getVideo } from '@/api'
import { formatDuration } from '@/lib/format'
import { isNotFoundError, videoRefetchInterval } from '@/lib/poll'

export default function VideoPage() {
  const { id } = useParams({ from: '/videos/$id' })
  const { pollMs } = useSearch({ from: '/videos/$id' })
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [deleteError, setDeleteError] = useState<string | null>(null)

  const query = useQuery({
    queryKey: ['video', id],
    queryFn: () => getVideo(id),
    retry: false,
    refetchInterval: (q) => videoRefetchInterval(q.state.data, q.state.error, pollMs),
  })

  async function handleDelete() {
    if (!window.confirm('Delete this video? This cannot be undone.')) return
    try {
      await deleteVideo(id)
      queryClient.invalidateQueries({ queryKey: ['videos'] })
      navigate({ to: '/' })
    } catch (err) {
      setDeleteError(err instanceof Error ? err.message : String(err))
    }
  }

  if (query.isError && isNotFoundError(query.error)) {
    return <p role="alert" className="text-sm text-destructive">Video not found.</p>
  }

  const video = query.data

  return (
    <>
      {query.isError && !isNotFoundError(query.error) && (
        <p role="alert" className="text-sm text-destructive">
          {query.error instanceof Error ? query.error.message : String(query.error)}
        </p>
      )}
      {video && (
        <Card>
          <CardHeader>
            <CardTitle>{video.title}</CardTitle>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            {video.status === 'ready' && video.master_playlist && <Player src={video.master_playlist} />}
            {video.status === 'failed' && (
              <p role="alert" className="text-sm text-destructive">Processing failed.</p>
            )}
            {video.status !== 'ready' && video.status !== 'failed' && (
              <p className="text-sm text-muted-foreground">Status: {video.status}</p>
            )}

            <dl className="grid grid-cols-2 gap-x-4 gap-y-1 text-sm text-muted-foreground">
              <dt>Duration</dt>
              <dd>{formatDuration(video.duration)}</dd>
              <dt>Resolution</dt>
              <dd>{video.width && video.height ? `${video.width}×${video.height}` : '--'}</dd>
            </dl>

            {deleteError && <p role="alert" className="text-sm text-destructive">{deleteError}</p>}
            <Button variant="destructive" className="self-start" onClick={handleDelete}>
              Delete
            </Button>
          </CardContent>
        </Card>
      )}
    </>
  )
}

function Player({ src }: { src: string }) {
  const ref = useRef<HTMLVideoElement>(null)

  useEffect(() => {
    const element = ref.current
    if (!element) return

    if (element.canPlayType('application/vnd.apple.mpegurl')) {
      element.src = src
      return
    }
    if (!Hls.isSupported()) return

    const hls = new Hls()
    hls.loadSource(src)
    hls.attachMedia(element)
    return () => hls.destroy()
  }, [src])

  return <video ref={ref} data-testid="player" data-src={src} controls className="w-full rounded-lg" />
}
```

Confirmation uses `window.confirm` rather than a shadcn dialog — a one-boolean decision doesn't justify installing and wiring a new primitive.

- [ ] **Step 8: Wire it into the route tree — modify `apps/web/src/router.tsx`**

```tsx
import {
  Outlet,
  createRootRoute,
  createRoute,
  createRouter,
  type RouterHistory,
} from '@tanstack/react-router'

import Dashboard from './routes/dashboard'
import VideoPage from './routes/video'

export const rootRoute = createRootRoute({
  component: () => (
    <main className="mx-auto flex max-w-3xl flex-col gap-6 p-8">
      <h1 className="font-heading text-2xl font-semibold">Video Thing</h1>
      <Outlet />
    </main>
  ),
})

export const dashboardRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  component: Dashboard,
})

export const videoRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: 'videos/$id',
  validateSearch: (search: Record<string, unknown>): { pollMs: number } => ({
    pollMs: Number(search.pollMs ?? 2000),
  }),
  component: VideoPage,
})

export const routeTree = rootRoute.addChildren([dashboardRoute, videoRoute])

export function createAppRouter(history?: RouterHistory) {
  return createRouter({ routeTree, ...(history ? { history } : {}) })
}

declare module '@tanstack/react-router' {
  interface Register {
    router: ReturnType<typeof createAppRouter>
  }
}
```

- [ ] **Step 9: Retire the now-empty placeholder test file**

Both routes render their real pages now; `router.tsx`'s wiring is exercised end-to-end by `dashboard.test.tsx` and `video.test.tsx`.

```bash
rm apps/web/src/router.test.tsx
```

- [ ] **Step 10: Run the whole web suite and lint**

Run: `cd apps/web && pnpm test && pnpm lint`
Expected: all PASS, clean.

- [ ] **Step 11: Drive it in a browser**

With the stack, API, and worker running (per `README.md`):

```bash
cd apps/web && pnpm dev
```

Open `http://localhost:5173`. Upload a file from the Dashboard; expect the browser to navigate to `/videos/<id>` and the status to progress to `ready` with a playable video. Go back to `/`, confirm the card shows duration and resolution, then open the video and click Delete; expect a confirm dialog, then a redirect back to `/` with the card gone.

- [ ] **Step 12: Commit**

```bash
git add apps/web/src/lib/poll.ts apps/web/src/lib/poll.test.ts apps/web/src/routes/video.tsx apps/web/src/routes/video.test.tsx apps/web/src/router.tsx
git rm apps/web/src/router.test.tsx
git commit -m "feat: add the video page with playback, metadata, and delete"
```

---

## Task 6: Documentation

**Files:**
- Modify: `README.md`
- Modify: `docs/specifications/vertical-slice-spec.md`

**Interfaces:**
- Consumes: everything from Tasks 1–5.
- Produces: nothing — documentation only.

- [ ] **Step 1: Update `README.md`'s status paragraph**

Replace:

```
**Status:** vertical slice implemented. `apps/api`, `apps/worker`, and `apps/web` run locally against LocalStack (S3 + SQS) and Postgres: a browser can upload a file, the worker transcodes it to 720p HLS with a thumbnail, and the page plays it back. `scripts/e2e.sh` proves the pipeline end to end from a cold stack. The full rendition ladder (only 720p exists today), deletion, listing, CloudFront, and deployment to AWS remain unbuilt — architecture, infrastructure, and API contract for that fuller scope are specified and the Terraform module tree is implemented and `terraform validate`-clean.
```

With:

```
**Status:** vertical slice implemented, plus listing, deletion, and a three-page frontend. `apps/api`, `apps/worker`, and `apps/web` run locally against LocalStack (S3 + SQS) and Postgres: a browser can upload a file from the Dashboard, the worker transcodes it to 720p HLS with a thumbnail, and the Dashboard and video-detail pages (TanStack Router/Query) list, play back, and delete it. `scripts/e2e.sh` proves the pipeline end to end from a cold stack. The full rendition ladder (only 720p exists today), CloudFront, and deployment to AWS remain unbuilt — architecture, infrastructure, and API contract for that fuller scope are specified and the Terraform module tree is implemented and `terraform validate`-clean.
```

- [ ] **Step 2: Update the repository-layout line for `apps/web`**

Replace:

```
    web/                Vite/React upload page with hls.js playback
```

With:

```
    web/                Vite/React dashboard, upload, and video-detail pages (TanStack Router/Query) with hls.js playback
```

- [ ] **Step 3: Remove the resolved row from `docs/specifications/vertical-slice-spec.md` §3**

Delete this line from the "Out of Scope" table (the Dashboard/video-detail/TanStack row — now built by this plan; the other rows are still owned by their respective plans):

```
| Dashboard and video-detail pages, TanStack Query/Router | web spec |
```

- [ ] **Step 4: Verify nothing else regressed**

Run: `cd apps/web && pnpm test && pnpm lint`
Expected: PASS, clean.

Run: `go build ./... && go vet ./... && gofmt -l .`
Expected: no output from any of the three (this plan touches no Go file, so this simply confirms the working tree is otherwise clean).

- [ ] **Step 5: Commit**

```bash
git add README.md docs/specifications/vertical-slice-spec.md
git commit -m "docs: mark the dashboard, video, and upload pages as built"
```
