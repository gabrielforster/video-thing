# FFmpeg Transcoding & HLS Packaging Profiles

This document specifies the exact FFmpeg/ffprobe invocations, rendition ladder, and HLS packaging rules the Worker Service uses to turn a single uploaded source file into an adaptive-bitrate HLS asset. It is the implementation-level companion to the Processing Flow in [`docs/architecture/sequence-diagrams.md`](../architecture/sequence-diagrams.md): that document covers the SQS/S3/Postgres orchestration around the worker; this one covers what happens inside the "ffprobe → transcode each rendition → package HLS → generate thumbnails" step.

## 1. Pipeline Overview

For each job the worker pulls off SQS, it runs a strictly ordered, single-pass-per-rendition pipeline:

1. **Probe.** `ffprobe` inspects the downloaded source (container, codec, resolution, duration, frame rate) to drive every downstream decision — most importantly, which renditions are eligible (§3).
2. **Transcode.** One `ffmpeg` invocation per eligible rendition produces H.264/AAC HLS segments and a variant playlist (§2, §5).
3. **Package.** The worker assembles `master.m3u8` referencing the variant playlists that were actually produced (§4).
4. **Thumbnail.** `ffmpeg` extracts a cover frame and a set of periodic preview frames (§6).
5. **Upload.** All artifacts are written to the processed bucket under a fixed key layout (below), then the DB row flips to `Ready`.

The output layout is fixed and every step in this document targets it:

```
processed/{video-id}/master.m3u8
processed/{video-id}/1080/playlist.m3u8
processed/{video-id}/1080/segment_00000.ts ... segment_NNNNN.ts
processed/{video-id}/720/playlist.m3u8
processed/{video-id}/720/segment_00000.ts ...
processed/{video-id}/480/playlist.m3u8
processed/{video-id}/480/segment_00000.ts ...
processed/{video-id}/360/playlist.m3u8
processed/{video-id}/360/segment_00000.ts ...
processed/{video-id}/thumbnails/cover.jpg
processed/{video-id}/thumbnails/5.jpg
processed/{video-id}/thumbnails/15.jpg
processed/{video-id}/thumbnails/25.jpg
...
```

Thumbnail filenames are the integer second offset into the source at which the frame was captured (`5.jpg` = frame at t=5s), which lets the frontend build a scrub-preview strip without a separate manifest.

## 2. Rendition Ladder

Exactly four renditions are defined. Bitrates follow Apple's HLS Authoring Specification guidance (target video bitrate roughly proportional to pixel count, with a "top rendition should comfortably fit common last-mile bandwidth" ceiling around 5 Mbps for 1080p AVC content) and standard VBV practice of capping `maxrate` at ~1.05–1.1x the target so CBR-like delivery doesn't blow through CDN/edge bitrate budgets on hard scenes, with `bufsize` set to `2x maxrate` (a common default that tolerates short-term complexity spikes — e.g. a scene cut — without triggering visible re-quantization artifacts, while still converging within roughly one GOP).

| Rendition | Resolution (W×H) | Video codec | Video bitrate (target / max via VBV) | Audio codec | Audio bitrate | Frame rate handling | Profile / Level |
|---|---|---|---|---|---|---|---|
| 1080p | 1920×1080 | H.264 (libx264) | 5000 kbps / 5350 kbps (bufsize 10700k) | AAC-LC | 128 kbps, 48 kHz, stereo | Passthrough (no `-r`); capped at 30fps for sources >30fps | High @ L4.1 |
| 720p | 1280×720 | H.264 (libx264) | 2800 kbps / 3000 kbps (bufsize 6000k) | AAC-LC | 128 kbps, 48 kHz, stereo | Passthrough; capped at 30fps | Main @ L3.1 |
| 480p | 854×480 | H.264 (libx264) | 1400 kbps / 1500 kbps (bufsize 3000k) | AAC-LC | 96 kbps, 48 kHz, stereo | Passthrough; capped at 30fps | Main @ L3.0 |
| 360p | 640×360 | H.264 (libx264) | 800 kbps / 850 kbps (bufsize 1700k) | AAC-LC | 96 kbps, 48 kHz, stereo | Passthrough; capped at 30fps | Baseline @ L3.0 |

Notes:

- **Bitrate scaling is intentionally sub-linear with resolution**, not proportional to raw pixel count: 1080p (2.07M px) is ~6.25x the pixels of 360p (0.23M px) but only ~6.25x the bitrate at the extremes is actually roughly consistent here (5000/800 ≈ 6.25), while the middle steps (720p→480p→360p) compress the ratio further because lower resolutions are cheaper per pixel to encode acceptably — this matches the shape of Apple's published bitrate ladder rather than a naive linear model.
- **maxrate/bufsize (VBV)** exist so a single encode is broadcast-safe over HTTP delivery: without a cap, x264's default ratecontrol can spike well above the target on high-motion segments, which risks the segment not being downloadable faster than real time on constrained connections. `-maxrate`/`-bufsize` bound the peak without forcing true CBR (which would waste bits on simple scenes).
- **Frame rate is passed through, not forced**, except for a 30fps ceiling: re-timestamping a 24fps source to 30fps burns bitrate on duplicated frames for no quality gain; the ceiling exists only to bound worst-case segment size/encode time for unusual 50/60fps sources.
- **Profile/level step down with rendition** because 360p/480p realistically need to remain decodable on very old or low-power hardware/software decoders (baseline/main), while 1080p targets modern decoders where High profile's extra compression efficiency (CABAC, 8x8 transforms) is worth spending on the bitrate-constrained top rendition.
- Audio is deliberately **not resolution-scaled beyond two tiers** (128k for 1080p/720p, 96k for 480p/360p) — AAC-LC quality degrades noticeably below ~96k for stereo music/speech mixes, so audio bitrate is floored independent of how far video bitrate drops.

## 3. Source-Resolution-Aware Rendition Selection

The worker never upscales. Producing a "1080p" rendition from a 720p source would waste storage/CDN bytes on fabricated detail and could mislead viewers (and player ABR logic) into thinking a higher-quality stream exists than actually does. Eligibility is therefore derived from the ffprobe result, not hardcoded.

**Rule:** a rendition is produced only if the source's shorter dimension (height, for standard landscape/portrait-agnostic comparison we use height directly since all ladder entries are defined by height) is **greater than or equal to** that rendition's height, with one exception — the *nearest-at-or-below* rendition is always produced even if the source is smaller than every ladder entry, so a very low-resolution source still gets at least one playable output.

Pseudocode as implemented in the worker:

```text
probe := ffprobe(source_file)              # width, height, duration, codec, r_frame_rate
source_height := probe.height

ladder := [1080, 720, 480, 360]            # descending, matches table in §2
eligible := []

for rendition_height in ladder:
    if rendition_height <= source_height:
        eligible.append(rendition_height)

if eligible is empty:
    # source is smaller than even 360p (e.g. a 240p upload) —
    # still produce exactly one rendition so playback isn't blocked
    eligible = [smallest_ladder_entry_closest_to(source_height)]  # i.e. 360

renditions_to_encode := eligible
```

Worked examples:

| Source height (ffprobe) | Renditions produced |
|---|---|
| 1080 (or higher, e.g. 4K/2160) | 1080, 720, 480, 360 |
| 720 | 720, 480, 360 (1080 skipped — would require upscale) |
| 480 | 480, 360 |
| 360 | 360 |
| 240 | 360 (fallback: nearest ladder entry, even though this technically upscales a 240p source — accepted tradeoff so the video is playable at all; flagged in worker logs as `upscaled_fallback=true`) |

`master.m3u8` only ever lists the renditions actually produced — a 720p source therefore yields a 3-variant master playlist, never a 4-variant one with a missing/broken 1080p entry.

## 4. HLS Packaging Parameters

| Parameter | Value | Justification |
|---|---|---|
| Segment duration | 6 seconds | Balances startup latency (shorter segments mean the player's first `GET` returns faster) against CDN request volume/cache overhead (longer segments mean fewer S3 origin requests behind CloudFront). 6s is also the exact granularity the platform's CloudFront distribution is tuned around: segments carry a 24h immutable TTL (see Playback Flow in `sequence-diagrams.md`), and 6s keeps per-segment byte size in a range (roughly 600KB–3.75MB across the ladder) that plays well with both range requests and edge cache eviction behavior. |
| Segment container/naming | MPEG-TS, `segment_%05d.ts` (e.g. `segment_00000.ts`, `segment_00001.ts`, …) | Plain `.ts` is chosen over fMP4/CMAF fragments for the MVP because it needs no `EXT-X-MAP` init segment handling and has the widest possible compatibility with older HLS clients/Safari without extra muxer flags — simplicity over the marginal byte savings and low-latency features fMP4 buys, which aren't needed for file-based VOD. The zero-padded numeric suffix is monotonic per rendition, making segments trivially content-addressable/immutable: a re-transcode of the same video-id always writes to a new `{video-id}` prefix (or a version-suffixed prefix), so `segment_NNNNN.ts` keys are never mutated in place — that immutability is exactly what licenses the 24h CDN TTL. |
| Playlist type | `VOD` (`#EXT-X-PLAYLIST-TYPE:VOD`) | This is file-based, finite-duration, already-fully-transcoded HLS, not a live/growing stream — `VOD` tells the player the entire segment list is present up front (no polling for playlist updates), and it's required for `#EXT-X-ENDLIST` to be honored correctly. `EVENT` (growing playlist) or omitting the tag (implicit live behavior) would incorrectly signal to hls.js that it should keep re-fetching the playlist for new segments. |
| Master playlist ordering | Ascending bitrate (lowest `BANDWIDTH` first, i.e. 360p, then 480p, 720p, 1080p) | Most HLS clients (including hls.js) pick their initial rendition based on the first playlist entry that fits estimated bandwidth, and several players/heuristics default to the *first* listed variant before the first buffer-based measurement completes. Listing ascending means a cold start (before ABR has any bandwidth estimate) begins on the smallest rendition — fast first-frame, no risk of stalling on a connection that turns out to be slow — and lets ABR step up once real throughput is measured, rather than risk starting on 1080p and immediately having to downshift. |
| `EXT-X-STREAM-INF` attributes | `BANDWIDTH` (peak, i.e. the VBV `maxrate` sum of video+audio, not the target bitrate), `RESOLUTION`, `CODECS` (RFC 6381 string, e.g. `avc1.640029,mp4a.40.2` for the 1080p High@L4.1 + AAC-LC pair) | `BANDWIDTH` is defined by the spec as the peak segment bitrate a client should provision for, so using `maxrate` (not the average target) avoids under-provisioning that causes rebuffering on high-motion segments. `CODECS` lets clients that support it skip a playlist fetch/parse round-trip to determine decodability. |
| Closed-GOP / keyframe alignment | GOP length = segment duration × frame rate (e.g. 6s × 30fps = 180 frames), closed GOP, forced IDR at GOP boundaries, identical GOP structure across all four renditions | HLS requires each `.ts` segment to be independently decodable starting at a keyframe; if GOP length doesn't divide evenly into the segment duration, segment boundaries and keyframes drift apart and the player either has to decode extra frames it then discards, or (worse) starts mid-GOP and shows corrupted/no video for a frame. Aligning GOP length identically **across renditions** is what makes clean, seamless bitrate switching possible: the player switches renditions only at segment boundaries, and since every rendition's segment N covers the exact same presentation-time range with a keyframe at its start, there's no visual glitch, duplicate frame, or gap on switch. This is the same alignment discipline CMAF was formalized to guarantee; the MVP uses plain TS instead of CMAF/fMP4, but still enforces the underlying alignment invariant by hand via matching `-g`/`-keyint_min`/`-sc_threshold 0`/`-hls_time` values across all four ffmpeg invocations. |

## 5. Example FFmpeg / ffprobe Commands

### 5.1 Probe stage

```bash
ffprobe -v error \
  -select_streams v:0 \
  -show_entries stream=width,height,r_frame_rate,codec_name,bit_rate \
  -show_entries format=duration,bit_rate \
  -of json \
  /work/source.mp4
```

Sample output the worker parses (fields it acts on: `width`/`height` for §3's rendition selection, `duration` for thumbnail interval math in §6, `codec_name` for logging/telemetry only — the source codec never changes what the worker does since everything is re-encoded regardless):

```json
{
  "streams": [
    {
      "codec_name": "h264",
      "width": 1920,
      "height": 1080,
      "r_frame_rate": "30000/1001"
    }
  ],
  "format": {
    "duration": "184.522000",
    "bit_rate": "6127412"
  }
}
```

### 5.2 Transcode + package one rendition (720p shown; other renditions differ only in the bolded-equivalent scale/bitrate/profile fields per §2)

```bash
ffmpeg -y -i /work/source.mp4 \
  -vf "scale=1280:720:force_original_aspect_ratio=decrease,pad=1280:720:(ow-iw)/2:(oh-ih)/2" \
  -c:v libx264 \
  -profile:v main -level:v 3.1 \
  -b:v 2800k -maxrate 3000k -bufsize 6000k \
  -r 30 \
  -x264-params "keyint=180:min-keyint=180:scenecut=0:open-gop=0" \
  -c:a aac -profile:a aac_low -b:a 128k -ar 48000 -ac 2 \
  -f hls \
  -hls_time 6 \
  -hls_playlist_type vod \
  -hls_flags independent_segments \
  -hls_segment_filename "processed/{video-id}/720/segment_%05d.ts" \
  "processed/{video-id}/720/playlist.m3u8"
```

Command notes:

- `scale=...force_original_aspect_ratio=decrease,pad=...` guarantees exact target dimensions without distorting aspect ratio, letterboxing instead of stretching non-16:9 sources.
- `-x264-params keyint=180:min-keyint=180:scenecut=0:open-gop=0` is the concrete mechanism behind §4's closed-GOP requirement: `keyint`/`min-keyint` fix GOP length to exactly `hls_time × fps` (here 6×30=180) with no variance, `scenecut=0` disables x264's adaptive keyframe insertion on scene cuts (which would otherwise break the fixed-GOP/segment alignment), and `open-gop=0` forces closed GOPs so each GOP decodes independently of neighboring ones — required for clean segment-boundary switching.
- `-hls_flags independent_segments` sets `#EXT-X-INDEPENDENT-SEGMENTS` in the playlist, asserting to the player that every segment can be decoded without any other segment — true here specifically because of the closed-GOP encode above.
- The 1080p/480p/360p invocations are byte-identical in structure; only `-vf scale=...`, `-profile:v`/`-level:v`, `-b:v`/`-maxrate`/`-bufsize`, `-b:a`, and the output path components change, per the §2 table. `keyint`/`min-keyint` stay at 180 (6s × 30fps) for every rendition so GOP boundaries line up across renditions per §4, even though bitrate/resolution differ.

### 5.3 Master playlist assembly

The worker does not use ffmpeg's built-in `-master_pl_name` (which requires driving every rendition from a single multi-output ffmpeg invocation via `-var_stream_map`); instead it runs one independent ffmpeg process per eligible rendition (as in §5.2) — this keeps per-rendition failures isolated and independently retryable/loggable, matching the per-stage-visible design described in the Processing Flow doc. The worker then hand-assembles `master.m3u8` in Go from the set of renditions that were actually produced (§3), ordered ascending by bandwidth (§4):

```
#EXTM3U
#EXT-X-VERSION:3
#EXT-X-STREAM-INF:BANDWIDTH=935000,RESOLUTION=640x360,CODECS="avc1.42001e,mp4a.40.2"
360/playlist.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=1596000,RESOLUTION=854x480,CODECS="avc1.4d001e,mp4a.40.2"
480/playlist.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=3128000,RESOLUTION=1280x720,CODECS="avc1.4d001f,mp4a.40.2"
720/playlist.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=5478000,RESOLUTION=1920x1080,CODECS="avc1.640029,mp4a.40.2"
1080/playlist.m3u8
```

(`BANDWIDTH` values above are each rendition's `maxrate` + audio bitrate, in bits/sec, per §4's peak-bandwidth rule.)

## 6. Thumbnail Generation

Two distinct thumbnail products come out of the same source file, both after the transcode stage completes (so a transcode failure doesn't leave orphaned thumbnails with no matching video, per the Processing Flow doc's "complete asset set in one upload batch" rule):

| Product | Seek point(s) | Command pattern | Notes |
|---|---|---|---|
| `cover.jpg` | 10% of `duration` (from ffprobe), clamped to a minimum of 1s so very short clips don't seek to t=0 (a common source of solid-black/logo-slate frames) | `ffmpeg -y -ss {seek} -i source.mp4 -vframes 1 -q:v 2 -vf "scale=1280:-2" cover.jpg` | `-ss` before `-i` uses fast input-seeking (keyframe-nearest, near-instant) rather than decode-and-discard seeking — acceptable here since exact frame accuracy doesn't matter for a representative cover image. `-q:v 2` targets high JPEG quality (libjpeg qscale, lower=better, 2 is visually near-lossless). |
| Periodic scrub thumbnails | Every 10 seconds of source duration, i.e. `t = 5, 15, 25, ...` (offset by half the interval so the first thumbnail isn't a t=0 black frame, matching `5.jpg`/`15.jpg` in the fixed key layout), **capped at 60 thumbnails** regardless of duration | Batch form: `ffmpeg -y -i source.mp4 -vf "fps=1/10,scale=320:-2" -vsync vfr -q:v 4 thumbnails/%d.jpg` then renamed to true second-offsets by the worker; equivalently, per-frame seek form: `ffmpeg -y -ss {t} -i source.mp4 -vframes 1 -q:v 4 -vf "scale=320:-2" thumbnails/{t}.jpg` | The worker uses the batch `fps=1/10` form for a single decode pass (cheaper than N separate seek-and-decode invocations for long videos), then renames sequential output (`1.jpg, 2.jpg, ...`) to true second-offsets (`5.jpg, 15.jpg, ...`) since `fps=1/10` samples starting at t=0, not t=5. The 60-thumbnail cap bounds worst-case storage/compute for very long uploads (a 10+ hour source would otherwise generate 3600+ tiny JPEGs); once the cap is hit the interval is widened dynamically (`interval = duration / 60`, rounded to the nearest 10s) rather than truncating coverage to only the first 10 minutes. |

Thumbnail JPEGs use `-q:v 4` (slightly lower quality than `cover.jpg`'s `-q:v 2`) since these are small scrub-preview images where a lower size:quality ratio matters more than for the single hero cover image.

## 7. Failure Handling

The worker treats ffprobe/ffmpeg outcomes as one of three buckets, which determine whether the SQS message is left for redelivery or the job is failed immediately (short-circuiting the retry loop described in the Failure and Retry Flow section of `sequence-diagrams.md`):

| Condition | Detection | Worker action |
|---|---|---|
| Corrupt / unsupported / non-media source | `ffprobe` exits non-zero, or exits 0 but returns no video stream, or stderr matches known-fatal patterns (`Invalid data found when processing input`, `moov atom not found`, `could not find codec parameters`) | **Fail fast** — do not retry. Immediately `UPDATE video SET status=failed`, then `DeleteMessage` so the SQS retry loop never engages for a file that will deterministically fail on every attempt. This is deliberate: letting `maxReceiveCount` exhaust itself on a poison-pill file just delays the terminal `Failed` status by the full redrive window for no benefit, and it burns Fargate task time that could serve real jobs. |
| Transient/environmental ffmpeg failure | Non-zero exit with stderr indicating a resource/environment problem (`Cannot allocate memory`, `No space left on device`, container OOM-kill signal, S3 GetObject/PutObject network error surfaced by the worker's SDK client) | **Retryable** — do *not* delete the SQS message and do *not* write `status=failed`; let the visibility timeout expire so SQS redelivers, per the existing retry flow. These failures are about the environment at that moment, not the input file, so a retry (possibly on a different Fargate task instance) has a real chance of succeeding. |
| Partial-rendition failure (e.g. 720p ffmpeg pass succeeds, 1080p pass crashes) | Any single rendition's ffmpeg exit code is non-zero after others succeeded | Treated as job failure for the whole video (not a partial `Ready`): the worker does not delete the message and does not mark `Ready`, since a `master.m3u8` missing an expected rendition is a worse user-facing outcome than a delayed retry. Whether the *specific* cause is fatal-input or transient-environment is then re-classified using the same two rules above on the retry attempt. |

`maxReceiveCount` and the DLQ redrive policy (defined at the SQS queue level, not in the worker) are the backstop for cases where a job is legitimately retryable per the rules above but keeps failing anyway (e.g. a consistently under-provisioned container size causing repeated OOM on a particular large source) — after the configured number of redeliveries, SQS moves the message to the DLQ automatically and the worker (or a DLQ-draining process) sets the terminal `status=failed`, exactly as described in the Failure and Retry Flow diagram. The fail-fast rule above exists specifically so that the *first* bucket (corrupt/unsupported input) never has to reach that DLQ path at all — it's a known-deterministic outcome, not something worth spending `maxReceiveCount` retries to rediscover.
