# Task 7: Update docs invalidated by this plan

> Task 7 of 7 in [`api-list-delete`](00-context.md). Read [`00-context.md`](00-context.md) first — the goal, tech stack, Global Constraints, and file structure bind this task. Full plan: [`api-list-delete-plan.md`](../../plans/api-list-delete-plan.md).
>
> Previous: [Task 6](task-06-extend-scripts-e2e-sh-listing-pagination.md)

---

**Files:**
- Modify: `README.md`
- Modify: `docs/specifications/vertical-slice-spec.md`

**Interfaces:**
- Consumes: nothing (documentation only).
- Produces: nothing consumed by later tasks — this is the last task.

- [ ] **Step 1: Update `README.md`'s status paragraph**

Replace the current status paragraph (which lists deletion and listing as unbuilt):

```markdown
**Status:** vertical slice implemented. `apps/api`, `apps/worker`, and `apps/web` run locally against LocalStack (S3 + SQS) and Postgres: a browser can upload a file, the worker transcodes it to 720p HLS with a thumbnail, and the page plays it back. `scripts/e2e.sh` proves the pipeline end to end from a cold stack. The full rendition ladder (only 720p exists today), deletion, listing, CloudFront, and deployment to AWS remain unbuilt — architecture, infrastructure, and API contract for that fuller scope are specified and the Terraform module tree is implemented and `terraform validate`-clean.
```

with:

```markdown
**Status:** vertical slice implemented, plus paginated listing, deletion (with S3 asset cleanup), and structured JSON logging (`docs/plans/api-list-delete-plan.md`). `apps/api`, `apps/worker`, and `apps/web` run locally against LocalStack (S3 + SQS) and Postgres: a browser can upload a file, the worker transcodes it to 720p HLS with a thumbnail, and the page plays it back; `GET /videos` lists videos with `limit`/`offset` pagination and `DELETE /videos/{id}` removes the row and its S3 assets. `scripts/e2e.sh` proves the pipeline, listing, and deletion end to end from a cold stack. The full rendition ladder (only 720p exists today), a dashboard UI, CloudFront, and deployment to AWS remain unbuilt — architecture, infrastructure, and API contract for that fuller scope are specified and the Terraform module tree is implemented and `terraform validate`-clean.
```

Also update the `apps/api` line in the "Repository layout" section:

```markdown
    api/                Gin service: presigned uploads, video CRUD, listing, deletion, health/readiness
```

replacing:

```markdown
    api/                Gin service: presigned uploads, video CRUD, health/readiness
```

- [ ] **Step 2: Update `docs/specifications/vertical-slice-spec.md` §3**

Replace the "Out of Scope" table:

```markdown
| Deferred | Owning spec |
|---|---|
| 1080p / 480p / 360p renditions, source-resolution-aware selection | worker spec |
| `DELETE /videos/{id}` and asset cleanup | api spec |
| `GET /videos` list, pagination | api spec |
| Dashboard and video-detail pages, TanStack Query/Router | web spec |
| CloudFront in the playback path | infrastructure spec |
| ECS deployment, CI/CD, monitoring, DLQ wiring | delivery spec |
```

with:

```markdown
| Deferred | Owning spec |
|---|---|
| 1080p / 480p / 360p renditions, source-resolution-aware selection | worker spec |
| Dashboard and video-detail pages, TanStack Query/Router | web spec |
| CloudFront in the playback path | infrastructure spec |
| ECS deployment, CI/CD, monitoring, DLQ wiring | delivery spec |

`DELETE /videos/{id}` (with S3 asset cleanup) and `GET /videos` (pagination) shipped in `docs/plans/api-list-delete-plan.md`.
```

- [ ] **Step 3: Verify no other doc references the old status**

```bash
grep -rn "deletion, listing\|GET /videos\` list, pagination" docs/ README.md
```
Expected: no matches (confirms the two edits above were the only stale references).

- [ ] **Step 4: Final full-repo check**

```bash
gofmt -l .
go vet ./...
go test ./...
cd apps/web && pnpm lint && pnpm test && cd ../..
```
Expected: all clean/green — this plan touches no `apps/web` code, so the web checks are a no-op confirmation that nothing else regressed.

- [ ] **Step 5: Commit**

```bash
git add README.md docs/specifications/vertical-slice-spec.md
git commit -m "docs: mark listing, deletion, and structured logging as shipped"
```
