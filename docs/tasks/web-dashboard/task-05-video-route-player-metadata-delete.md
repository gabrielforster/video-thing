# Task 5: Video route — player, metadata, delete

> Task 5 of 6 in [`web-dashboard`](00-context.md). Read [`00-context.md`](00-context.md) first — the goal, tech stack, Global Constraints, and file structure bind this task. Full plan: [`web-dashboard-plan.md`](../../plans/web-dashboard-plan.md).
>
> Previous: [Task 4](task-04-upload-flow-dashboard.md) · Next: [Task 6](task-06-documentation.md)

---

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
