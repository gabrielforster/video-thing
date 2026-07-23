# ADR-0004: CloudFront for CDN

## Status
Accepted

## Context
Processed video assets (HLS master playlists, variant playlists, and media segments) live in a private S3 bucket after the worker fleet finishes transcoding. These assets need to be served globally to viewers with low latency, and different asset types within the same HLS structure have very different cacheability:

- The **master playlist** (`.m3u8` listing available renditions) can change if renditions are added or re-encoded, so it needs a short TTL.
- **Variant playlists** (per-rendition `.m3u8`) update as new segments become available during processing but stabilize once processing completes, warranting a medium TTL.
- **Media segments** (`.ts`/`.m4s`) are immutable once written — a given segment URL never changes content — and can be cached essentially forever.

The platform is already committed to AWS for compute (ECS Fargate), queueing (SQS), and storage (S3), and to keeping the origin bucket private rather than publicly readable, per general security posture for user-uploaded/derived content.

## Decision
Put **Amazon CloudFront** in front of the processed-assets S3 bucket, using **Origin Access Control (OAC)** so the bucket itself remains fully private and is only readable via CloudFront's service identity. Cache behaviors are configured per path pattern to match the differentiated TTL strategy: master playlist paths get a 5-second TTL, variant playlist paths get a 30-second TTL, and segment paths get a 24-hour (effectively immutable) TTL.

## Alternatives Considered

- **Serving directly from S3** — Simplest possible setup: point clients at S3 URLs (or a bucket website endpoint) directly. Rejected because it has no edge caching, meaning every segment request — the vast majority of HLS traffic by volume — hits S3 directly regardless of viewer location, adding latency for distant viewers and scaling S3 request costs linearly with viewer count instead of being absorbed by edge cache hits for popular content. It also requires either a public bucket (undesirable) or pre-signed URLs for every object (operationally heavier and awkward for adaptive players issuing many segment requests per second).

- **Third-party CDN (Cloudflare, Fastly)** — Both are mature, capable CDNs with strong cache control and often better default pricing on egress than CloudFront. Rejected for this platform specifically (not as a general judgment against them) because they introduce a second vendor and billing relationship, and lose the tight native integration CloudFront has with S3 via OAC — with a third-party CDN, keeping the origin bucket private typically means signed URLs/cookies issued by the CDN provider or a custom auth header pattern, more moving parts than OAC's native IAM-based trust. Given the platform is already all-in on AWS for compute, queueing, and storage, adding a non-AWS CDN is a second operational surface (separate DNS/cert management, separate observability, separate support relationship) for a benefit (marginally better pricing or edge POP count) that doesn't outweigh the integration cost at MVP stage.

- **Self-hosted edge caching** (e.g., running Varnish/nginx cache nodes in multiple regions) — Rejected outright as reinventing a CDN from scratch. There is no operational or cost justification for building and maintaining a multi-region caching layer when mature managed CDNs exist; this would only make sense at a scale and cost-sensitivity level far beyond an MVP.

## Consequences

### Positive
- Per-path cache behaviors map directly onto the HLS asset hierarchy: master playlist (5s TTL), variant playlists (30s TTL), and segments (24h, immutable) can each be expressed as a distinct CloudFront cache behavior matched by path pattern, giving fine-grained control without custom logic.
- OAC keeps the S3 bucket fully private — no public bucket policy, no pre-signed URL generation needed for every segment — while CloudFront's own access controls (signed URLs/cookies, if needed later) can layer on top for content protection.
- Edge caching absorbs the overwhelming majority of segment requests (the highest-volume, most cacheable traffic), reducing both latency for viewers and direct S3 request load/cost.
- Stays within the AWS ecosystem already used for compute/queue/storage, meaning one IAM model, one observability stack (CloudWatch), and one billing relationship for the whole infrastructure.

### Negative / Tradeoffs
- CloudFront adds another layer of cache invalidation to reason about: a bug in TTL configuration (e.g., caching the master playlist too long) can serve stale rendition lists to viewers, and diagnosing "is this a CloudFront cache issue or an origin issue" adds a debugging step that direct S3 serving wouldn't have.
- CloudFront distribution changes (new cache behaviors, OAC policy updates) can take minutes to propagate globally, which slows iteration during infrastructure changes compared to an S3-only setup.
- AWS's CDN pricing and edge POP footprint, while adequate, are not uniformly the cheapest or most geographically dense option in every region compared to specialized CDNs — this is an accepted tradeoff for integration simplicity, not a claim that CloudFront is best-in-class on every axis.

## Notes
Revisit if the platform needs signed/tokenized playback URLs for access control (CloudFront signed URLs/cookies are a natural next step and compose with the existing OAC setup) or if traffic patterns show significant viewership concentrated in a region where a specialized CDN has meaningfully better edge presence than CloudFront. Revisit the specific TTL values once real playback and re-encode patterns are observed in production — they are a reasonable starting point, not tuned against real traffic yet.
