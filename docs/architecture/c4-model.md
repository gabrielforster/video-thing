# C4 Model: Video Thing

This document describes the Video Thing architecture using the [C4 model](https://c4model.com/) at three levels of abstraction: System Context, Container, and Component. It reflects the MVP scope of the system: video upload, asynchronous processing (transcoding, packaging, thumbnailing), and HLS playback. Authentication, live streaming, DRM, and analytics are explicitly out of MVP scope and are noted only where they provide useful future context.

---

## Level 1: System Context

The context diagram shows the Video Thing as a single system and the actors and external services that interact with it from the outside.

```mermaid
C4Context
    title System Context diagram for Video Thing

    Person(user, "End User / Content Uploader", "An anonymous visitor who both uploads videos and watches them. MVP has no authentication, so these are modeled as one actor.")

    System(videoThing, "Video Thing", "Allows a user to upload a video, have it transcoded into adaptive-bitrate renditions, and stream it back via HLS.")

    System_Ext(s3, "AWS S3", "Object storage for raw uploads and processed HLS assets (thumbnails, manifests, segments).")
    System_Ext(cloudfront, "AWS CloudFront", "CDN that serves processed video assets to viewers with low latency.")
    System_Ext(sqs, "AWS SQS", "Durable queue that decouples upload completion from asynchronous processing.")

    Rel(user, videoThing, "Uploads videos to, and watches videos from", "HTTPS")
    Rel(videoThing, s3, "Stores raw uploads and processed assets in", "HTTPS / AWS API")
    Rel(videoThing, sqs, "Publishes and consumes processing jobs via", "AWS API")
    Rel(cloudfront, s3, "Fetches processed assets from (origin)", "HTTPS")
    Rel(user, cloudfront, "Streams HLS video from", "HTTPS")

    UpdateLayoutConfig($c4ShapeInRow="3", $c4BoundaryInRow="1")
```

### Actors

- **End User (viewer)** — watches videos served as HLS streams through CloudFront.
- **Content Uploader** — submits raw video files for processing.

In the MVP, these are **the same actor**. There is no authentication, no accounts, and no authorization model: any visitor can request an upload URL and any visitor can watch any processed video whose ID they have. This is called out explicitly here rather than left implicit, because it is the single biggest simplification in the system's threat model and UX, and it is the first place a reviewer should look when assessing production readiness.

### System purpose

The Video Thing accepts a video file from a user, uploads it directly to object storage, processes it in the background into multiple HLS renditions and thumbnails, and serves the result back through a CDN for adaptive playback. The system boundary here includes the API and worker logic that constitutes "the platform"; S3, CloudFront, and SQS are drawn as external systems because they are managed AWS services the platform depends on and configures, not code the platform team owns or deploys.

### MVP boundary vs. future context

Out of scope for MVP, mentioned here only as future context:

- **Authentication** — would insert as a new external identity provider (e.g., an IdP box) with a `Rel` from the User to it, and the User→Video Thing relationship would carry a bearer token/session cookie instead of being anonymous. It would also introduce an owner concept on uploaded videos, which does not exist today.
- **Live streaming** — would introduce a new ingest protocol (RTMP/SRT) and a separate low-latency delivery path; not modeled here.
- **DRM** — would sit between CloudFront and the viewer (license server interaction) and is not represented.
- **Analytics** — would add an event-collection external system fed by both the frontend player and CloudFront access logs; not represented.

---

## Level 2: Container Diagram

The container diagram zooms into the Video Thing system boundary and shows the deployable/runnable units and the managed AWS services they talk to directly.

```mermaid
C4Container
    title Container diagram for Video Thing

    Person(user, "End User / Content Uploader", "Anonymous user; uploads and watches videos.")

    System_Boundary(videoThing, "Video Thing") {
        Container(spa, "Web Frontend", "React, Vite, TypeScript, hls.js", "Single-page app for uploading videos and playing back HLS streams in the browser.")
        Container(api, "API Service", "Go, Gin", "Stateless REST API. Issues presigned upload URLs, exposes video metadata/status, enqueues processing jobs, serves health checks.")
        Container(worker, "Worker Service", "ECS Fargate, Go, FFmpeg", "Long-running background service that polls SQS and runs the transcode/package/thumbnail pipeline for each uploaded video.")
        ContainerDb(db, "PostgreSQL Database", "AWS RDS for PostgreSQL", "Stores video records, processing status, and rendition/asset metadata.")
        ContainerQueue(queue, "SQS Queue", "AWS SQS", "Durable work queue carrying one processing job per uploaded video.")
        ContainerDb(s3raw, "S3 Raw Bucket", "AWS S3", "Landing zone for user-uploaded source video files.")
        ContainerDb(s3processed, "S3 Processed Bucket", "AWS S3", "Holds HLS manifests, segments, and generated thumbnails.")
        Container(cdn, "CloudFront Distribution", "AWS CloudFront", "CDN edge cache in front of the processed assets bucket.")
    }

    Rel(user, spa, "Uses", "HTTPS")
    Rel(spa, api, "Requests upload URLs, video metadata, and status", "HTTPS / REST (JSON)")
    Rel(spa, s3raw, "Uploads raw video file directly to", "Presigned HTTPS PUT")
    Rel(spa, cdn, "Requests HLS manifest and segments for playback", "HTTPS (HLS via hls.js)")

    Rel(api, db, "Reads/writes video and job metadata", "SQL (pgx)")
    Rel(api, s3raw, "Generates presigned PUT URLs for (does not proxy bytes)", "AWS API (SDK)")
    Rel(api, queue, "Enqueues a processing job after upload is registered", "AWS API (SendMessage)")

    Rel(s3raw, queue, "Notifies on object-created", "S3 Event Notification")
    Rel(worker, queue, "Polls for and deletes processing jobs", "AWS API (ReceiveMessage/DeleteMessage)")
    Rel(worker, s3raw, "Downloads source video from", "AWS API (SDK, HTTPS)")
    Rel(worker, s3processed, "Uploads HLS renditions and thumbnails to", "AWS API (SDK, HTTPS)")
    Rel(worker, db, "Updates processing status and asset metadata", "SQL (pgx)")

    Rel(cdn, s3processed, "Origin-fetches processed assets from", "HTTPS")

    UpdateLayoutConfig($c4ShapeInRow="4", $c4BoundaryInRow="1")
```

### Container responsibilities

- **Web Frontend** — a React/Vite/TypeScript SPA. Requests a presigned upload URL from the API, PUTs the raw file straight to S3, then polls the API for processing status and uses `hls.js` to play the resulting manifest served via CloudFront.
- **API Service** — a stateless Go/Gin REST service. Its only jobs are: mint presigned S3 URLs, persist/read video and job metadata in PostgreSQL, and enqueue processing jobs onto SQS. It never touches video bytes.
- **Worker Service** — an ECS Fargate task running Go orchestration around FFmpeg/ffprobe. It polls SQS, downloads the source, transcodes to multiple renditions, packages HLS, generates thumbnails, uploads results, and updates the database — entirely decoupled from request/response latency.
- **PostgreSQL (RDS)** — the system of record for video and job state, shared (via distinct access patterns) by the API and the worker.
- **SQS Queue** — the sole coupling point between "upload accepted" and "processing happens." Provides durability, retry, and backpressure without the API or worker needing to know about each other.
- **S3 Raw Bucket** — write target for browser uploads and read source for the worker; not routed through the API.
- **S3 Processed Bucket** — write target for the worker and origin for CloudFront; never written to directly by the API or frontend.
- **CloudFront Distribution** — the only path viewers use to fetch HLS assets; caches at the edge and shields the processed bucket from direct public access.

### Key design principles

- **Never proxy uploads through the API.** The API's role in the upload path is limited to authorizing (implicitly, given no-auth MVP) and generating a presigned PUT URL. The actual file bytes flow browser → S3 directly. This keeps the API stateless, avoids buffering large files in application memory, and lets upload throughput scale independently of API capacity.
- **The worker never blocks the API.** The API's synchronous responsibility ends the moment a job is placed on SQS. Transcoding is CPU- and time-intensive (minutes, not milliseconds); SQS is the boundary that lets the worker fail, retry, and scale (via Fargate task count) without any HTTP request ever waiting on FFmpeg.

---

## Level 3: Component Diagrams

### API Service — Components

```mermaid
C4Component
    title Component diagram for API Service

    Container(spa, "Web Frontend", "React, Vite, TypeScript", "Calls the API over HTTPS.")
    ContainerDb(db, "PostgreSQL Database", "AWS RDS", "Video and job metadata.")
    ContainerQueue(queue, "SQS Queue", "AWS SQS", "Processing job queue.")
    ContainerDb(s3raw, "S3 Raw Bucket", "AWS S3", "Raw upload target.")

    Container_Boundary(api, "API Service") {
        Component(router, "Router / Handlers", "Gin Engine + Middleware", "Top-level HTTP routing, request logging, panic recovery, JSON binding/validation.")
        Component(uploadHandler, "Upload Handler", "Gin handler", "Generates a presigned S3 PUT URL and registers a pending video record.")
        Component(videoHandler, "Video Handler", "Gin handler", "Lists videos, returns video detail/status, exposes playback manifest URL.")
        Component(healthHandler, "Health Handler", "Gin handler", "Liveness/readiness endpoint for ECS/ALB health checks.")
        Component(repo, "Repository Layer", "sqlc-generated Go", "Type-safe SQL queries for videos and processing jobs.")
        Component(pgPool, "pgx Connection Pool", "pgx/v5 pgxpool", "Manages pooled PostgreSQL connections used by the repository layer.")
        Component(presignClient, "S3 Presign Client", "AWS SDK v2 s3.PresignClient", "Produces time-limited, method-scoped presigned URLs for direct browser upload.")
        Component(sqsClient, "SQS Client", "AWS SDK v2 sqs.Client", "Publishes processing job messages after an upload is registered.")
    }

    Rel(spa, router, "HTTP requests", "HTTPS / JSON")
    Rel(router, uploadHandler, "Routes POST /uploads to")
    Rel(router, videoHandler, "Routes GET /videos... to")
    Rel(router, healthHandler, "Routes GET /health to")

    Rel(uploadHandler, presignClient, "Requests a presigned PUT URL from")
    Rel(uploadHandler, repo, "Inserts pending video record via")
    Rel(uploadHandler, sqsClient, "Enqueues processing job via")
    Rel(videoHandler, repo, "Reads video/status rows via")

    Rel(repo, pgPool, "Executes queries through")
    Rel(pgPool, db, "SQL", "TCP/5432")
    Rel(presignClient, s3raw, "Signs URLs scoped to (no bytes transferred)", "AWS API")
    Rel(sqsClient, queue, "SendMessage", "AWS API")

    UpdateLayoutConfig($c4ShapeInRow="4", $c4BoundaryInRow="1")
```

**Request flow (upload):** `spa` → `router` → `uploadHandler`, which calls `presignClient` to mint a scoped, time-limited PUT URL, calls `repo` (backed by `pgPool`) to persist a `pending` video row, and calls `sqsClient` to publish the job — all before returning the presigned URL to the browser, which then uploads directly to `s3raw`.

**Request flow (status/playback):** `spa` → `router` → `videoHandler` → `repo` → `pgPool` → `db`, returning current processing status and, once complete, the CloudFront URL of the HLS manifest.

The API is intentionally thin: the `Router/Handlers` layer does no business logic beyond binding/validation, handlers delegate all persistence to the `sqlc`-generated `Repository Layer` (compile-time-checked SQL, no ORM), and the `pgx` pool is the single shared resource injected into every handler that needs data access. The `S3 Presign Client` and `SQS Client` are the only two points where the API touches AWS services directly, and both are narrow, single-purpose wrappers — the API never opens a data-plane connection to S3 for object bytes.

### Worker Service — Components

```mermaid
C4Component
    title Component diagram for Worker Service

    ContainerQueue(queue, "SQS Queue", "AWS SQS", "Processing job queue.")
    ContainerDb(s3raw, "S3 Raw Bucket", "AWS S3", "Raw upload source.")
    ContainerDb(s3processed, "S3 Processed Bucket", "AWS S3", "HLS + thumbnail destination.")
    ContainerDb(db, "PostgreSQL Database", "AWS RDS", "Video and job metadata.")

    Container_Boundary(worker, "Worker Service") {
        Component(poller, "SQS Poller", "Go, long-polling loop", "Continuously receives processing job messages from SQS.")
        Component(dispatcher, "Job Dispatcher", "Go", "Parses each message into a job and drives it through the pipeline stages in order.")
        Component(downloader, "Downloader", "AWS SDK v2 S3 client", "Fetches the raw source video into local/ephemeral storage.")
        Component(prober, "Prober", "ffprobe wrapper", "Inspects the source file for codec, resolution, duration, and stream metadata.")
        Component(transcoder, "Transcoder", "ffmpeg wrapper", "Produces multiple bitrate/resolution renditions from the source.")
        Component(hlsPackager, "HLS Packager", "ffmpeg/ffmpeg-hls wrapper", "Segments each rendition and builds master + media .m3u8 playlists.")
        Component(thumbnailGen, "Thumbnail Generator", "ffmpeg wrapper", "Extracts preview thumbnail image(s) from the source or a rendition.")
        Component(uploader, "Uploader", "AWS SDK v2 S3 client", "Uploads HLS playlists, segments, and thumbnails to the processed bucket.")
        Component(dbUpdater, "DB Status Updater", "sqlc-generated repository", "Writes final asset locations and marks the video's processing status.")
    }

    Rel(poller, queue, "ReceiveMessage (long poll)", "AWS API")
    Rel(poller, dispatcher, "Hands off parsed job to")

    Rel(dispatcher, downloader, "1. Download source")
    Rel(downloader, s3raw, "GetObject", "AWS API")

    Rel(dispatcher, prober, "2. Probe source")
    Rel(prober, downloader, "Reads local file from")

    Rel(dispatcher, transcoder, "3. Transcode renditions")
    Rel(transcoder, prober, "Uses probe metadata from")

    Rel(dispatcher, hlsPackager, "4. Package HLS")
    Rel(hlsPackager, transcoder, "Segments output of")

    Rel(dispatcher, thumbnailGen, "5. Generate thumbnails")
    Rel(thumbnailGen, downloader, "Reads local file from")

    Rel(dispatcher, uploader, "6. Upload assets")
    Rel(uploader, hlsPackager, "Reads playlists/segments from")
    Rel(uploader, thumbnailGen, "Reads thumbnail(s) from")
    Rel(uploader, s3processed, "PutObject", "AWS API")

    Rel(dispatcher, dbUpdater, "7. Update status")
    Rel(dbUpdater, db, "SQL (pgx)", "TCP/5432")

    Rel(dispatcher, poller, "8. Signal success -> delete message")
    Rel(poller, queue, "DeleteMessage", "AWS API")

    UpdateLayoutConfig($c4ShapeInRow="3", $c4BoundaryInRow="1")
```

**Pipeline flow (matches processing order):** the `SQS Poller` long-polls `queue` and hands each message to the `Job Dispatcher`, which drives a strict, ordered pipeline: **(1) Download** the source via the `Downloader`; **(2) Probe** it with the `Prober` (ffprobe) to extract codec/resolution/duration; **(3) Transcode** multiple renditions with the `Transcoder` (ffmpeg) using the probe output to pick sane encode parameters; **(4) Package HLS** by segmenting each rendition and generating master/media playlists via the `HLS Packager`; **(5) Generate thumbnails** from the source with the `Thumbnail Generator`; **(6) Upload** all resulting playlists, segments, and thumbnails to `s3processed` via the `Uploader`; **(7) Update DB** status and asset paths via the `DB Status Updater`; and only **(8) after every prior stage succeeds**, the dispatcher signals the poller to delete the SQS message.

This strict ordering and the deliberate placement of `DeleteMessage` as the last step are the key reliability decisions in the worker: if the process crashes or any stage fails partway through, the message becomes visible again after the visibility timeout and the whole job is retried from scratch rather than being lost or silently half-completed. Each component wraps a single external tool or AWS API surface (ffprobe, ffmpeg, S3, SQS, Postgres) so failures are isolated and attributable to a specific pipeline stage, which is also where future work like partial-failure handling or per-stage retries would be inserted without touching the API.
