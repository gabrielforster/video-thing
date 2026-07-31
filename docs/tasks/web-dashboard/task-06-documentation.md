# Task 6: Documentation

> Task 6 of 6 in [`web-dashboard`](00-context.md). Read [`00-context.md`](00-context.md) first — the goal, tech stack, Global Constraints, and file structure bind this task. Full plan: [`web-dashboard-plan.md`](../../plans/web-dashboard-plan.md).
>
> Previous: [Task 5](task-05-video-route-player-metadata-delete.md)

---

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
