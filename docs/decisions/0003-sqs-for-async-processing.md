# ADR-0003: SQS for Async Processing

## Status
Accepted

## Context
Video upload and transcoding are decoupled by design: a user (or client) uploads a source file to S3, and that upload must trigger asynchronous processing by a worker fleet without blocking the upload path. The system needs:

- At-least-once delivery of "process this video" jobs to a worker fleet that scales elastically (see ADR-0002).
- A natural backpressure/scaling signal so ECS can scale worker task count up under load and down to near-zero when idle.
- A failure/retry story: transient ffmpeg or S3 errors should retry a bounded number of times, and jobs that repeatedly fail should land somewhere for inspection rather than looping forever or silently vanishing.
- Low operational overhead — this is glue infrastructure between an S3 event and a worker poll loop, not a differentiated part of the product.

Ordering across videos does not matter (each video's processing is independent), and there is no requirement to replay a historical stream of events for reprocessing or analytics.

## Decision
Use **Amazon SQS** as the queue between the S3 upload event (via S3 Event Notifications) and the worker fleet. Workers long-poll the queue; failed messages become visible again after the visibility timeout expires and are retried up to a configured `maxReceiveCount`, after which the redrive policy moves them to a Dead Letter Queue (DLQ) for inspection.

## Alternatives Considered

- **Amazon Kinesis Data Streams** — Offers ordered, replayable event streams and can fan out to multiple consumers. Rejected because none of those properties are needed here: video processing jobs are independent and unordered, and there's no requirement to replay history for a second consumer. Kinesis also requires managing shard count (or on-demand mode) and consumer checkpointing (via KCL or a DynamoDB-backed offset table), which is meaningfully more operational complexity than SQS for a workload that is a pure work queue, not an event log.

- **Amazon MQ / self-managed RabbitMQ** — Would give more sophisticated routing (exchanges, topic-based routing) if the platform later needs multiple job types routed to different consumer pools. Rejected for MVP because SQS already provides everything needed (at-least-once delivery, visibility timeouts, DLQ) as a fully managed, near-zero-maintenance service, whereas Amazon MQ still requires broker instance sizing, patching cadence decisions, and HA configuration — operational surface with no corresponding benefit at this stage.

- **S3 event → Lambda → ECS RunTask per video** — Would remove the queue entirely: each upload event directly invokes a Lambda that calls `ecs:RunTask` to spin up a dedicated task per video. Rejected because this tightly couples upload rate to task creation rate with no natural buffering or concurrency control — a burst of uploads would attempt to launch a burst of tasks simultaneously, fighting Fargate capacity and ECS API rate limits, and there's no clean backpressure signal if downstream capacity is saturated. SQS's queue depth is a much cleaner scaling and backpressure primitive than "count of Lambda invocations attempting RunTask calls."

- **Custom Postgres-based job queue** — Would avoid introducing another AWS service, and the platform already runs Postgres for metadata. Rejected because it means reimplementing, by hand, semantics SQS provides for free: at-least-once delivery, per-message visibility timeouts (so a crashed worker's in-flight job becomes retryable), exponential backoff on redelivery, and dead-letter handling. A `SELECT ... FOR UPDATE SKIP LOCKED`-based queue is a well-known pattern, but it adds contention on the primary database under load and requires building and testing failure-mode behavior that SQS has already hardened in production for over a decade.

## Consequences

### Positive
- Visibility timeout + redrive-to-DLQ maps directly onto the platform's failure/retry flow: a worker that dies mid-job (task killed, ffmpeg crash) simply lets the message become visible again after timeout, and no custom crash-recovery logic is needed.
- `ApproximateNumberOfMessagesVisible` is a first-class CloudWatch metric that Application Auto Scaling can target directly, giving a clean, native scale-with-queue-depth signal for the ECS worker service without custom metric plumbing.
- Fully managed: no brokers to patch, size, or fail over.
- Decouples upload latency from processing latency entirely — the upload path only needs to succeed at writing to S3 and (indirectly) enqueuing a message, not at completing any processing.

### Negative / Tradeoffs
- At-least-once delivery means workers must be idempotent — reprocessing the same video twice (e.g., after a visibility timeout expiry followed by a late ack) must not corrupt output or double-charge resources.
- No strict ordering guarantee; if a future feature needs strict per-user or per-video ordering (e.g., "always process replace-uploads after original uploads"), SQS standard queues won't provide it (FIFO queues would, at a throughput cost).
- SQS is a pure work queue, not an event log — if the platform later wants to replay or reprocess historical upload events for a new feature, that history doesn't exist in SQS and would need to be sourced from elsewhere (e.g., S3 object listing or a database record).
- DLQ messages require an operational habit of monitoring and triage; a DLQ that silently accumulates is just a slower version of silently dropping jobs.

## Notes
Revisit if the platform needs multiple distinct job types with different routing/priority needs (at which point SQS's simplicity may need supplementing with per-job-type queues, which is a straightforward extension), or if strict ordering per video/user becomes a requirement (move the relevant queue to FIFO). Revisit the DLQ/redrive configuration specifically once real failure-mode data exists in production — the max receive count and backoff strategy should be tuned from observed transient-vs-permanent failure rates, not guessed upfront.
