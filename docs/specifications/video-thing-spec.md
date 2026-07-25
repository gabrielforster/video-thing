# Video Thing MVP - Technical Specification (v2)

Status: Accepted
Supersedes: v1 (informal draft)

## 1. Overview

This document defines the architecture, infrastructure, repository organization, and implementation plan for the first version of a cloud-native video platform.

The primary goal of the MVP is to allow users to:

* Upload videos
* Process uploaded videos into adaptive bitrate HLS
* Generate thumbnails
* Browse uploaded videos
* Play videos using adaptive streaming
* Deliver all content through a CDN

The platform is designed from the beginning to evolve into a complete streaming platform — including live streaming, DRM, analytics, authentication, and monetization — without requiring architectural rewrites. See [Section 17](#17-future-roadmap-out-of-mvp) and the individual ADRs in `docs/decisions/` for how each technology choice was made with that evolution in mind.

v2 changes: this revision adds the level of detail expected for a production build-out — sequence diagrams, a C4 architecture model, a complete Terraform module tree, an OpenAPI contract, database migrations, FFmpeg encoding profiles, and ADRs for every major technology decision. The narrative sections below (2–19) are unchanged from v1 in intent; where a topic now has a dedicated deep-dive document, this file links out rather than duplicating content.

---

## 2. Design Principles

**Cloud Native.** Every component is independently deployable.

**Event Driven.** Long-running operations never block API requests.

**Horizontally Scalable.** Every service scales independently.

**Stateless Services.** All services are disposable.

**Infrastructure as Code.** Every cloud resource is provisioned using Terraform ([ADR-0005](../decisions/0005-terraform-for-infrastructure.md)).

**Immutable Deployments.** Services are deployed through CI/CD using immutable Docker images.

---

## 3. MVP Scope

Included:

* Video upload
* Presigned uploads to S3
* Video processing
* Thumbnail generation
* Multi-bitrate HLS generation
* Metadata persistence
* Video listing
* Video playback
* CloudFront CDN
* Admin-like frontend

Not included (see [Section 17](#17-future-roadmap-out-of-mvp)):

* Authentication / Authorization
* Live Streaming
* DRM
* Video Analytics
* Comments / Likes / Search
* Video Editing
* Subtitles
* Multiple Organizations
* Payments

---

## 4. High-Level Architecture

```text
Browser -> POST /videos -> Go API Service -> generate presigned URL -> Raw Upload Bucket (S3)
Raw Upload Bucket -> S3 ObjectCreated event -> SQS -> ECS Fargate Worker Service
Worker: download original -> FFmpeg (renditions + thumbnails) -> upload processed assets -> Processed Video Bucket (S3)
Processed Video Bucket -> CloudFront Distribution -> Browser (hls.js playback)
```

For the full request/response and failure-path detail behind this diagram, see:

* **[Sequence Diagrams](../architecture/sequence-diagrams.md)** — Upload, Processing, Playback, and Failure/Retry flows as Mermaid sequence diagrams.
* **[C4 Model](../architecture/c4-model.md)** — System Context, Container, and Component (API + Worker) diagrams.

---

## 5. Technology Stack

| Layer | Choice | ADR |
|---|---|---|
| Backend language | Go, Gin, sqlc, pgx | [ADR-0001](../decisions/0001-go-for-backend-services.md) |
| Compute | ECS Fargate, Application Auto Scaling | [ADR-0002](../decisions/0002-ecs-fargate-for-compute.md) |
| Queue | Amazon SQS | [ADR-0003](../decisions/0003-sqs-for-async-processing.md) |
| Storage | Amazon S3 (raw uploads, processed assets) | — |
| CDN | CloudFront | [ADR-0004](../decisions/0004-cloudfront-for-cdn.md) |
| Database | PostgreSQL (Amazon RDS in prod) | — |
| Video processing | FFmpeg | — |
| Infrastructure | Terraform | [ADR-0005](../decisions/0005-terraform-for-infrastructure.md) |
| Delivery format | HLS, hls.js | [ADR-0006](../decisions/0006-hls-for-video-delivery.md) |
| Frontend | React, Vite, TypeScript, TanStack Query/Router, Tailwind, shadcn/ui | — |

---

## 6. Repository Structure

```text
video-thing/
    apps/
        api/
        worker/
        web/
    packages/
        database/
        contracts/
        shared/
    infrastructure/
        terraform/
            modules/
            environments/
                dev/
                staging/
                production/
    docker/
    scripts/
    docs/
        architecture/
        decisions/
        specifications/
```

This spec's own supporting documents live under `docs/` following that same layout:

```text
docs/
    architecture/
        c4-model.md
        sequence-diagrams.md
    decisions/
        0001-go-for-backend-services.md
        0002-ecs-fargate-for-compute.md
        0003-sqs-for-async-processing.md
        0004-cloudfront-for-cdn.md
        0005-terraform-for-infrastructure.md
        0006-hls-for-video-delivery.md
    specifications/
        video-thing-spec.md        (this file)
        openapi.yaml
        database-schema.md
        ffmpeg-profiles.md
        migrations/
            000001_create_videos_table.up.sql
            000001_create_videos_table.down.sql
```

And the Terraform module tree is implemented (not just described) under `infrastructure/terraform/`:

```text
infrastructure/terraform/
    modules/
        networking/    iam/           ecr/          alb/
        ecs/           rds/           s3/           cloudfront/
        sqs/           logs/          monitoring/
    environments/
        dev/           (main.tf wiring all modules together, validated with `terraform validate`)
        staging/       (directory reserved; not yet implemented — copy dev/*.tf and adjust tfvars)
        production/    (directory reserved; not yet implemented — copy dev/*.tf, set multi_az=true, skip_final_snapshot=false)
```

---

## 7. Backend Services

### API Service

Responsibilities: CRUD videos, generate upload URLs, return metadata, return playback URLs, health endpoints.

Never performs: FFmpeg processing, thumbnail generation, long-running jobs.

Full contract: **[OpenAPI specification](openapi.yaml)** — `POST /videos`, `GET /videos`, `GET /videos/{id}`, `DELETE /videos/{id}`, `POST /videos/{id}/complete`, `GET /healthz`, `GET /readyz`.

### Worker Service

Responsibilities: consume SQS messages and run the processing pipeline (download → transcode → package HLS → generate thumbnails → upload → update DB → delete queue message). Fully stateless — any task can pick up any message.

Encoding details: **[FFmpeg profiles](ffmpeg-profiles.md)** — rendition ladder (1080p/720p/480p/360p), HLS packaging parameters, thumbnail generation, and failure classification.

---

## 8. Infrastructure

Terraform module tree and environment layout are defined in [Section 6](#6-repository-structure) above and implemented under `infrastructure/terraform/`. Each module exposes a fixed variable/output interface documented in its own `variables.tf`/`outputs.tf`; `environments/dev/main.tf` is the reference wiring showing how modules compose (networking → iam/ecr/s3/sqs → cloudfront → rds → alb → ecs → monitoring).

Rationale for Terraform itself, and for each individual AWS service choice, is in the ADRs listed in [Section 5](#5-technology-stack).

---

## 9. Upload Flow

```text
Browser -> POST /videos -> API -> create DB record -> generate presigned URL -> return URL
Browser -> uploads directly to S3 -> S3 Event -> SQS -> Worker
```

No uploaded video ever passes through the API. See the **Upload Flow** diagram in [sequence-diagrams.md](../architecture/sequence-diagrams.md) for the full message sequence, and [ADR-0003](../decisions/0003-sqs-for-async-processing.md) for why SQS sits between the S3 event and the worker.

---

## 10. Processing Flow

Worker receives:

```json
{
    "videoId": "...",
    "bucket": "...",
    "key": "uploads/file.mp4"
}
```

Worker stages: download original → validate file → probe metadata → generate thumbnails → generate renditions → package HLS → upload processed assets → update database → mark Ready.

Full stage-by-stage detail: **[Processing Flow sequence diagram](../architecture/sequence-diagrams.md#processing-flow)** and **[FFmpeg profiles](ffmpeg-profiles.md)**.

---

## 11. Output Structure

```text
processed/
    {video-id}/
        master.m3u8
        1080/
        720/
        480/
        360/
        thumbnails/
            cover.jpg
            5.jpg
            15.jpg
```

Exact per-rendition bitrates, codecs, segment duration, and playlist construction rules are defined in [ffmpeg-profiles.md](ffmpeg-profiles.md).

---

## 12. Database

Schema, enum-vs-check-constraint rationale, indexing, the `updated_at` trigger convention, and the golang-migrate migration strategy are fully specified in **[database-schema.md](database-schema.md)**, with the reference migration at `migrations/000001_create_videos_table.{up,down}.sql`.

Summary of the `videos` table: `id`, `title`, `status` (uploading / processing / ready / failed), `duration`, `width`, `height`, `size_bytes`, `master_playlist`, `thumbnail`, `source_bucket`, `source_key`, `error_message`, `created_at`, `updated_at`.

---

## 13. Frontend MVP

Pages:

**Dashboard** — list uploaded videos; each card shows thumbnail, duration, resolution, status.

**Upload** — drag & drop, progress bar, status.

**Video Page** — player, metadata. Resolution switching handled automatically by hls.js ([ADR-0006](../decisions/0006-hls-for-video-delivery.md)).

---

## 14. ECS Worker Scaling

Workers scale from queue depth via `ApproximateNumberOfMessagesVisible`, using Application Auto Scaling target tracking (implemented in `infrastructure/terraform/modules/ecs`). Workers scale back to zero when idle to minimize cost. See [ADR-0002](../decisions/0002-ecs-fargate-for-compute.md) for why Fargate (vs. Lambda or self-managed EC2) fits this scale-to-zero model.

---

## 15. CDN Strategy

CloudFront cache behavior by content type:

| Content | TTL |
|---|---|
| Master playlist | 5 seconds |
| Variant playlists | 30 seconds |
| Segments | 24 hours (immutable filenames) |

Implemented as three `ordered_cache_behavior` blocks in `infrastructure/terraform/modules/cloudfront`. Rationale: [ADR-0004](../decisions/0004-cloudfront-for-cdn.md).

---

## 16. Observability

**Logging** — structured JSON, correlation IDs.

**Metrics**

* API: requests, errors, latency.
* Worker: processing duration, queue latency, failures.
* Infrastructure: queue depth, ECS scaling, CPU, memory.

Implemented via `infrastructure/terraform/modules/logs` (CloudWatch Log Groups) and `infrastructure/terraform/modules/monitoring` (dashboard + alarms on queue depth, ALB 5xx rate, ECS CPU).

---

## 17. Future Roadmap (Out of MVP)

### Live Streaming

**Goal:** allow broadcasters to stream live video, distributed globally through CloudFront using adaptive HLS.

```text
OBS -> RTMP Ingest -> Streaming Service -> FFmpeg Packaging -> HLS -> CloudFront -> Players
```

Possible RTMP ingest servers: SRS, Nginx-RTMP. Future AWS-managed alternative: AWS MediaLive / AWS MediaPackage.

Requirements: multiple ingest endpoints, DVR support, low-latency HLS, stream recording, stream health monitoring, automatic stream lifecycle, live chat integration (future), automatic VOD generation after stream ends.

[ADR-0006](../decisions/0006-hls-for-video-delivery.md) covers why building on HLS now doesn't block this — LL-HLS is an extension of the same format.

### Authentication

JWT, OIDC, OAuth2. See [C4 Model](../architecture/c4-model.md) for where an auth boundary would insert itself into the current context/container diagrams.

### Video Analytics

Track: view count, watch duration, bitrate selection, buffering events, device information, geographic distribution.

### DRM

Support Widevine, FairPlay, PlayReady. [ADR-0006](../decisions/0006-hls-for-video-delivery.md) covers why HLS doesn't block this (FairPlay is HLS-native; Widevine/PlayReady can be layered on via sample-aes or CMAF).

### Subtitle Pipeline

Automatic subtitle extraction, multiple languages, translation.

### AI Features

Automatic chapter generation, scene detection, thumbnail recommendation, speech transcription, semantic search, content moderation.

---

## 18. CI/CD

Each service builds independently:

```text
Git Push -> Tests -> Docker Build -> Push to ECR -> Terraform Plan -> Terraform Apply -> Deploy ECS Services
```

Database migrations run as an explicit pipeline step before deploying new API/worker versions — never via application-side auto-migration in production. See [database-schema.md](database-schema.md#migration-strategy).

---

## 19. Success Criteria

The MVP is considered complete when:

* A user can upload a video directly to S3 using a presigned URL.
* The upload automatically triggers asynchronous processing.
* FFmpeg generates multiple HLS renditions and thumbnails per [ffmpeg-profiles.md](ffmpeg-profiles.md).
* Processed assets are stored in the processed bucket.
* CloudFront serves the HLS master playlist and segments per the [CDN strategy](#15-cdn-strategy).
* The frontend displays uploaded videos and their processing status.
* Videos can be played in the browser using adaptive bitrate streaming via hls.js.
* ECS Fargate workers automatically scale based on SQS queue depth, per [Section 14](#14-ecs-worker-scaling).
* All AWS infrastructure is provisioned exclusively through Terraform (validated: `infrastructure/terraform/environments/dev` passes `terraform validate`).
* The architecture cleanly supports future additions — authentication, live streaming, analytics, DRM — without requiring significant redesign, per [Section 17](#17-future-roadmap-out-of-mvp) and the individual ADRs.

---

## 20. Document Index

| Document | Contents |
|---|---|
| [architecture/c4-model.md](../architecture/c4-model.md) | C4 Context, Container, and Component (API + Worker) diagrams |
| [architecture/sequence-diagrams.md](../architecture/sequence-diagrams.md) | Upload, Processing, Playback, Failure/Retry sequence diagrams |
| [specifications/openapi.yaml](openapi.yaml) | OpenAPI 3.0.3 contract for the API service |
| [specifications/database-schema.md](database-schema.md) | Schema, indexing, trigger convention, migration strategy |
| [specifications/migrations/](migrations/) | golang-migrate reference migration (000001) |
| [specifications/ffmpeg-profiles.md](ffmpeg-profiles.md) | Rendition ladder, HLS packaging, thumbnails, failure classification |
| [decisions/0001-go-for-backend-services.md](../decisions/0001-go-for-backend-services.md) | Why Go over Node.js/Python/Rust |
| [decisions/0002-ecs-fargate-for-compute.md](../decisions/0002-ecs-fargate-for-compute.md) | Why ECS Fargate over EKS/EC2/Lambda |
| [decisions/0003-sqs-for-async-processing.md](../decisions/0003-sqs-for-async-processing.md) | Why SQS over Kinesis/MQ/custom queue |
| [decisions/0004-cloudfront-for-cdn.md](../decisions/0004-cloudfront-for-cdn.md) | Why CloudFront over direct S3/third-party CDN |
| [decisions/0005-terraform-for-infrastructure.md](../decisions/0005-terraform-for-infrastructure.md) | Why Terraform over CDK/CloudFormation/Pulumi/ClickOps |
| [decisions/0006-hls-for-video-delivery.md](../decisions/0006-hls-for-video-delivery.md) | Why HLS over DASH/progressive MP4/RTMP |
| `infrastructure/terraform/modules/*` | Implemented Terraform modules (networking, iam, ecr, alb, ecs, rds, s3, cloudfront, sqs, logs, monitoring) |
| `infrastructure/terraform/environments/dev/main.tf` | Reference module wiring, `terraform validate`-clean |
