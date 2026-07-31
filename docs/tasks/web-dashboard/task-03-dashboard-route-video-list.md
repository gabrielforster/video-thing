# Task 3: Dashboard route — video list

> Task 3 of 6 in [`web-dashboard`](00-context.md). Read [`00-context.md`](00-context.md) first — the goal, tech stack, Global Constraints, and file structure bind this task. Full plan: [`web-dashboard-plan.md`](../../plans/web-dashboard-plan.md).
>
> Previous: [Task 2](task-02-api-client-list-delete.md) · Next: [Task 4](task-04-upload-flow-dashboard.md)

---

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
