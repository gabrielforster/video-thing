# Task 4: Upload flow on the dashboard

> Task 4 of 6 in [`web-dashboard`](00-context.md). Read [`00-context.md`](00-context.md) first — the goal, tech stack, Global Constraints, and file structure bind this task. Full plan: [`web-dashboard-plan.md`](../../plans/web-dashboard-plan.md).
>
> Previous: [Task 3](task-03-dashboard-route-video-list.md) · Next: [Task 5](task-05-video-route-player-metadata-delete.md)

---

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
