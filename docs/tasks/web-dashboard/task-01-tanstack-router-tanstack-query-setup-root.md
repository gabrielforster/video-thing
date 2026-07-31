# Task 1: TanStack Router + TanStack Query setup, root layout, retire the single page

> Task 1 of 6 in [`web-dashboard`](00-context.md). Read [`00-context.md`](00-context.md) first — the goal, tech stack, Global Constraints, and file structure bind this task. Full plan: [`web-dashboard-plan.md`](../../plans/web-dashboard-plan.md).
>
> Next: [Task 2](task-02-api-client-list-delete.md)

---

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
