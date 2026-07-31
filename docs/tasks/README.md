# Tasks

One folder per plan in [`../plans/`](../plans/README.md), one file per task. The
task files are the plans' `## Task N` sections verbatim — nothing was rewritten
in the split. Each folder's `00-context.md` carries the plan's goal, tech stack,
Global Constraints, and file structure, plus an index of its tasks; every task
file links back to it.

Hand a single task file to an implementer. Do not hand over a whole plan.

| Plan | Tasks | Folder |
|---|---|---|
| worker rendition ladder | 7 | [`worker-rendition-ladder/`](worker-rendition-ladder/00-context.md) |
| api list/delete/logging | 7 | [`api-list-delete/`](api-list-delete/00-context.md) |
| web dashboard | 6 | [`web-dashboard/`](web-dashboard/00-context.md) |
| delivery | 9 | [`delivery/`](delivery/00-context.md) |

`vertical-slice-plan.md` is not split — it is already implemented.

## Order

Within a folder, tasks run in number order: each one's tests depend on the
previous one's code, and each ends with a commit.

Across folders, `worker-rendition-ladder` and `api-list-delete` are
independent — either order, or in parallel. `web-dashboard` needs
`api-list-delete` landed, because it calls those endpoints. `delivery` can
start at any point.

## Before executing anything

Read [`../plans/README.md`](../plans/README.md) — it holds the six cross-plan
contracts (wire shapes, query names, delete ordering, asset URLs, no new
columns, logging), the files two plans both touch, and the accepted gaps that
must not be "fixed" inside a task without a decision.
