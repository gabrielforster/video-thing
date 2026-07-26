# Video Thing

Cloud-native video platform: upload, transcode to adaptive-bitrate HLS, deliver via CDN. Event-driven, horizontally scalable, deployed on AWS via Terraform.

**Status:** vertical slice implemented. `apps/api`, `apps/worker`, and `apps/web` run locally against LocalStack (S3 + SQS) and Postgres: a browser can upload a file, the worker transcodes it to 720p HLS with a thumbnail, and the page plays it back. `scripts/e2e.sh` proves the pipeline end to end from a cold stack. The full rendition ladder (only 720p exists today), deletion, listing, CloudFront, and deployment to AWS remain unbuilt — architecture, infrastructure, and API contract for that fuller scope are specified and the Terraform module tree is implemented and `terraform validate`-clean.

## Run it locally

Requires Docker, Go 1.25+ (the module targets 1.25.5), Node 20+, ffmpeg, awscli, jq, and golang-migrate.

```bash
make up          # compose stack, wait for Postgres + SQS, run migrations
make e2e         # one-shot proof the whole pipeline works
```

To drive it from the browser, run the three services in three terminals:

```bash
make api         # :8080
make worker      # derives the LocalStack queue URL itself
make web         # :5173
```

The Makefile exports the local defaults every target needs (`DATABASE_URL`,
`AWS_ENDPOINT_URL`, the two bucket names, `PUBLIC_ASSET_BASE_URL`, `PORT`);
override any of them on the command line, e.g. `make api PORT=9090`. The web app
reads `VITE_API_URL`, defaulting to `http://localhost:8080`.

Without a browser, the same flow is four calls — note the upload URL comes back
as `upload.uploadUrl`, and the `Content-Type` must match the header the API
returns, since it is signed into the URL:

```bash
ID_AND_URL=$(curl -s -X POST localhost:8080/videos \
  -H 'content-type: application/json' -d '{"title":"manual test"}' \
  | jq -r '.video.id, .upload.uploadUrl')
ID=$(echo "$ID_AND_URL" | head -1); URL=$(echo "$ID_AND_URL" | tail -1)
curl -s -X PUT --upload-file clip.mp4 -H 'content-type: application/octet-stream' "$URL"
curl -s -X POST "localhost:8080/videos/$ID/complete"   # optional; the S3 event drives processing
curl -s "localhost:8080/videos/$ID" | jq              # poll until status is "ready"
```

`make down` stops the stack. It keeps the Postgres volume, so `make up` is
resumable; LocalStack is in-memory, so a restart re-runs the init script and
drops any previously uploaded objects.

## Start here

[`docs/specifications/video-thing-spec.md`](docs/specifications/video-thing-spec.md) is the entry point — architecture, tech stack, flows, and links to every other document below.

## Documents

| Path | Contents |
|---|---|
| [docs/specifications/video-thing-spec.md](docs/specifications/video-thing-spec.md) | Master spec: architecture, scope, tech stack, flows |
| [docs/architecture/c4-model.md](docs/architecture/c4-model.md) | C4 Context, Container, and Component diagrams |
| [docs/architecture/sequence-diagrams.md](docs/architecture/sequence-diagrams.md) | Upload, completion, processing, playback, failure/retry, deletion flows |
| [docs/specifications/openapi.yaml](docs/specifications/openapi.yaml) | OpenAPI 3.0.3 contract for the API service |
| [docs/specifications/database-schema.md](docs/specifications/database-schema.md) | Schema, indexing, migration strategy (golang-migrate) |
| [docs/specifications/ffmpeg-profiles.md](docs/specifications/ffmpeg-profiles.md) | HLS rendition ladder, packaging, thumbnails |
| [docs/decisions/](docs/decisions/) | ADRs: Go, ECS Fargate, SQS, CloudFront, Terraform, HLS |

## Infrastructure

Terraform modules under `infrastructure/terraform/modules/`: `networking`, `iam`, `ecr`, `alb`, `ecs`, `rds`, `s3`, `cloudfront`, `sqs`, `logs`, `monitoring`. Wired together for a dev deployment in `infrastructure/terraform/environments/dev/`.

```
cd infrastructure/terraform/environments/dev
terraform init
terraform plan
```

`staging/` and `production/` environment directories are reserved but not yet implemented — copy `dev/*.tf` and adjust `.tfvars` (see the spec's [Section 6](docs/specifications/video-thing-spec.md#6-repository-structure)).

## Repository layout

```
docs/
    architecture/       C4 model, sequence diagrams
    decisions/           ADRs
    specifications/       specs, OpenAPI, DB schema, FFmpeg profiles
    plans/                implementation plans
infrastructure/
    terraform/
        modules/          11 reusable modules
        environments/      dev (implemented), staging/production (reserved)
apps/
    api/                Gin service: presigned uploads, video CRUD, health/readiness
    worker/             SQS consumer: ffmpeg transcode to 720p HLS, thumbnails, DB updates
    web/                Vite/React upload page with hls.js playback
packages/
    database/           sqlc-generated queries + golang-migrate migrations
                        (migrations/, queries.sql, sqlc.yaml, db/)
scripts/
    e2e.sh              cold-stack end-to-end proof (see "Run it locally")
```

`packages/contracts` and `packages/shared` from the original design were deliberately not created — the vertical slice turned out not to need a separate generated-types package or a shared-code package; `apps/api` and `apps/worker` each import `packages/database` directly.
