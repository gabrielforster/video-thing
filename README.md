# Video Thing

Cloud-native video platform: upload, transcode to adaptive-bitrate HLS, deliver via CDN. Event-driven, horizontally scalable, deployed on AWS via Terraform.

**Status:** design phase. Architecture, infrastructure, and API contract are specified and the Terraform module tree is implemented and `terraform validate`-clean. Application code (`apps/api`, `apps/worker`, `apps/web`) is not written yet.

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
    specifications/       spec, OpenAPI, DB schema, migrations, FFmpeg profiles
infrastructure/
    terraform/
        modules/          11 reusable modules
        environments/      dev (implemented), staging/production (reserved)
apps/                     not yet implemented — api, worker, web
packages/                 not yet implemented — database, contracts, shared
```
