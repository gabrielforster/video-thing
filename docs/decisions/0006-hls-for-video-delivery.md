# ADR-0006: HLS for Video Delivery

## Status
Accepted

## Context
The platform needs to deliver on-demand video to browsers with adaptive bitrate switching so viewers on varying network conditions get a usable experience without manual quality selection. The MVP's scope is video-on-demand (VOD) playback, not live streaming, but the platform's stated longer-term goal is to evolve into a full streaming platform, so the delivery format chosen now should not force an architectural rewrite when live streaming, low-latency requirements, or DRM become priorities. The team also wants to avoid doubling storage and transcoding cost by packaging output in more than one adaptive streaming format without a concrete MVP requirement driving it.

## Decision
Use **HLS (HTTP Live Streaming)**, multi-bitrate, played in-browser via **hls.js**, as the sole delivery format for the MVP. Do not package a second format (e.g., DASH) in parallel, and do not build a live-streaming-first architecture — the pipeline targets VOD delivery of pre-transcoded, segmented HLS output.

## Alternatives Considered

- **MPEG-DASH** — Comparable adaptive streaming capability to HLS, with more codec flexibility (native support for VP9/AV1 without vendor-specific extensions) and an open standard not originated by a single vendor. Rejected as the sole or primary format because native playback support is asymmetric across platforms: Safari and iOS support HLS natively (including in embedded webviews) but do not natively support DASH, meaning a DASH-only or DASH-primary strategy would require an additional compatibility layer or fallback path for a significant share of viewers. Packaging both DASH and HLS in parallel to get the best of both was considered and rejected for the MVP specifically because it roughly doubles segment storage and, more importantly, doubles the transcoding/packaging work the worker fleet does per video, for a codec-flexibility benefit (e.g., AV1) that isn't a current MVP requirement.

- **Progressive MP4 download** — The simplest possible delivery mechanism: transcode to a single MP4 and let the browser's native `<video>` tag handle playback via HTTP range requests. Rejected because it has no adaptive bitrate switching — a viewer on a degraded connection gets buffering or has to manually pick a lower-resolution file, and there's no clean mechanism for switching resolution mid-playback. This is a materially worse viewing experience on variable networks (mobile especially) than adaptive streaming provides, and doesn't meaningfully simplify the pipeline enough to justify the UX regression, since the worker already needs to produce multiple renditions for adaptive playback quality tiers regardless of container/manifest format.

- **Proprietary RTMP-based delivery to viewers** — Historically the default for live streaming, with mature server-side tooling. Rejected outright for VOD delivery to a browser player: modern browsers do not natively play RTMP without a plugin (Flash, long since deprecated and removed), making it the wrong tool for delivering to a browser-based `<video>`/hls.js player regardless of its merits for server-to-server ingest. RTMP remains a reasonable choice for *ingest* in a future live-streaming feature, but that's a separate concern from viewer-facing delivery, which is what this decision covers.

## Consequences

### Positive
- Single delivery format means the worker fleet only needs one packaging step per transcoded rendition, keeping the transcoding pipeline, storage footprint, and CloudFront cache-behavior configuration (ADR-0004) simple.
- hls.js gives broad browser coverage (everywhere HLS isn't natively supported, i.e., outside Safari/iOS) via Media Source Extensions, so a single client-side player library covers effectively all target browsers.
- Native Safari/iOS HLS support means no compatibility shim is needed for a significant and growing share of mobile viewers.
- Does not close off future direction: FairPlay DRM is HLS-native, so adding Apple-ecosystem DRM later doesn't require a format change. Widevine/PlayReady can be layered onto HLS via sample-aes encryption or by moving segment packaging to CMAF (which HLS can reference), so cross-platform DRM is a packaging addition, not an architectural rewrite. Low-Latency HLS (LL-HLS) is an extension of the same HLS format (partial segments, blocking playlist reload) rather than a different protocol, so a future live/low-latency feature builds on the same pipeline rather than replacing it.

### Negative / Tradeoffs
- Locking into HLS as the sole format means any future codec that only ships well-supported DASH tooling (some AV1/VP9 workflows are DASH-first in tooling maturity) may require revisiting this decision or adding DASH packaging alongside HLS at that point.
- hls.js adds a client-side JavaScript dependency and MSE-based playback complexity for non-Safari browsers, versus what would be truly native playback everywhere if the ecosystem were more uniform.
- Segment-based delivery (regardless of HLS vs. DASH) inherently has higher latency than genuinely low-latency protocols (e.g., WebRTC) — acceptable for VOD and even for LL-HLS-based near-real-time live, but not a fit if the platform later wants sub-second-latency interactive live streaming, which would need a different delivery mechanism entirely for that specific feature.

## Notes
Revisit if the platform adds a codec (e.g., AV1) whose encoder/packaging tooling is materially more mature for DASH/CMAF than for HLS, or if a live-streaming feature demands latency lower than LL-HLS can provide (at which point a WebRTC-based delivery path would need to be added alongside, not instead of, HLS for VOD). Revisit DRM format support (FairPlay vs. Widevine/PlayReady layering) once a concrete content-protection requirement exists — the current decision only confirms HLS doesn't block that future work, not that DRM packaging is implemented now.
