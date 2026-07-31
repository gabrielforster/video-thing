# Task 2: API client — list and delete

> Task 2 of 6 in [`web-dashboard`](00-context.md). Read [`00-context.md`](00-context.md) first — the goal, tech stack, Global Constraints, and file structure bind this task. Full plan: [`web-dashboard-plan.md`](../../plans/web-dashboard-plan.md).
>
> Previous: [Task 1](task-01-tanstack-router-tanstack-query-setup-root.md) · Next: [Task 3](task-03-dashboard-route-video-list.md)

---

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
