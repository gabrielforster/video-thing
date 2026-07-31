# Task 7: End-to-end ladder proof and documentation

> Task 7 of 7 in [`worker-rendition-ladder`](00-context.md). Read [`00-context.md`](00-context.md) first — the goal, tech stack, Global Constraints, and file structure bind this task. Full plan: [`worker-rendition-ladder-plan.md`](../../plans/worker-rendition-ladder-plan.md).
>
> Previous: [Task 6](task-06-periodic-scrub-thumbnails.md)

---

**Files:**
- Modify: `scripts/e2e.sh:233-237` (the single-variant grep) and `scripts/e2e.sh:264-297` (the asset assertions and the PASS banner)
- Modify: `README.md` (status paragraph, repository-layout worker line)
- Modify: `docs/specifications/vertical-slice-spec.md` (§3 deferred row)
- Modify: `docs/specifications/ffmpeg-profiles.md` (§5.3's 360p `BANDWIDTH`)

**Interfaces:**
- Consumes: the running pipeline from Tasks 1-6 and the existing `e2e.sh` helpers `require_nonempty_object` and `queue_depth`, plus the variables `$ID`, `$TMP`, `$PROCESSED_BUCKET`, `$AWS_ENDPOINT_URL`, `$MASTER_LEN`, `$MASTER_URL`.
- Produces: no code interfaces. `scripts/e2e.sh` gains `$EXPECTED_VARIANTS`.

The script already generates a `1280x720` `testsrc` clip, which is exactly the interesting case: §3 makes 720/480/360 eligible and 1080 an upscale, so a passing run proves both the ladder *and* the no-upscale rule in one assertion. The four-variant playlist is pinned byte-for-byte by the unit test in Task 4; spending three extra 1080p encodes of e2e wall-clock to re-prove it buys nothing. The 10-second clip yields exactly one scrub frame (`fps=1/10` emits its first frame at t=0, renamed to `5.jpg`), so asserting the thumbnail set is exactly `cover.jpg` plus `5.jpg` proves the rename and the cap in one check.

- [ ] **Step 1: Replace the single-variant grep in `scripts/e2e.sh`**

Replace these five lines (currently 233-237):

```bash
if ! grep -q '720/playlist\.m3u8' "$TMP/master.m3u8"; then
    echo "FAIL: master playlist does not reference the 720p variant playlist ($MASTER_KEY):" >&2
    cat "$TMP/master.m3u8" >&2
    exit 1
fi
```

with:

```bash
EXPECTED_VARIANTS="360 480 720"

VARIANTS="$(grep -oE '^[0-9]+/playlist\.m3u8' "$TMP/master.m3u8" | cut -d/ -f1 | tr '\n' ' ' | sed 's/ *$//')"
if [ "$VARIANTS" != "$EXPECTED_VARIANTS" ]; then
    echo "FAIL: master playlist lists variants [$VARIANTS], want [$EXPECTED_VARIANTS] ($MASTER_KEY)" >&2
    echo "      the 1280x720 source makes 720/480/360 eligible; 1080 would be an upscale" >&2
    echo "      (ffmpeg-profiles.md section 3), and the order must ascend by bandwidth (section 4)" >&2
    cat "$TMP/master.m3u8" >&2
    exit 1
fi

PREV_BANDWIDTH=0
for bandwidth in $(grep -oE 'BANDWIDTH=[0-9]+' "$TMP/master.m3u8" | cut -d= -f2); do
    if [ "$bandwidth" -le "$PREV_BANDWIDTH" ]; then
        echo "FAIL: master playlist BANDWIDTH values are not ascending ($bandwidth after $PREV_BANDWIDTH):" >&2
        cat "$TMP/master.m3u8" >&2
        exit 1
    fi
    PREV_BANDWIDTH="$bandwidth"
done
```

- [ ] **Step 2: Replace the asset assertions and the PASS banner**

Replace everything from `COVER_LEN="$(require_nonempty_object ...` to the end of the file (currently 264-297) with:

```bash
COVER_LEN="$(require_nonempty_object "processed/$ID/thumbnails/cover.jpg" "cover thumbnail")"
SCRUB_LEN="$(require_nonempty_object "processed/$ID/thumbnails/5.jpg" "scrub thumbnail at t=5s")"

aws --endpoint-url "$AWS_ENDPOINT_URL" s3api list-objects-v2 \
    --bucket "$PROCESSED_BUCKET" --prefix "processed/$ID/thumbnails/" \
    --query 'Contents[].Key' --output text >"$TMP/thumbnails.txt"
THUMBS="$(tr '\t' '\n' <"$TMP/thumbnails.txt" \
    | sed "s|processed/$ID/thumbnails/||" | LC_ALL=C sort | tr '\n' ' ' | sed 's/ *$//')"
if [ "$THUMBS" != "5.jpg cover.jpg" ]; then
    echo "FAIL: thumbnails under processed/$ID/thumbnails/ are [$THUMBS], want [5.jpg cover.jpg]" >&2
    echo "      a 10s source yields exactly one scrub frame, at t=5s, and ffmpeg's sequential" >&2
    echo "      output must have been renamed to that true second offset" >&2
    exit 1
fi

TOTAL_SEGMENTS=0
for dir in $EXPECTED_VARIANTS; do
    PLAYLIST_LEN="$(require_nonempty_object "processed/$ID/$dir/playlist.m3u8" "${dir}p rendition playlist")"

    aws --endpoint-url "$AWS_ENDPOINT_URL" s3api list-objects-v2 \
        --bucket "$PROCESSED_BUCKET" --prefix "processed/$ID/$dir/" \
        --query 'Contents[].[Key,Size]' --output text >"$TMP/rendition-$dir.txt"

    SEGMENTS=0
    while IFS=$'\t' read -r key size; do
        [ -n "$key" ] || continue
        case "$key" in
            */segment_*.ts)
                if [ "${size:-0}" -le 0 ]; then
                    echo "FAIL: segment $key is zero-length" >&2
                    exit 1
                fi
                SEGMENTS=$((SEGMENTS + 1))
                ;;
        esac
    done <"$TMP/rendition-$dir.txt"

    if [ "$SEGMENTS" -lt 2 ]; then
        echo "FAIL: expected at least 2 segment_*.ts objects under processed/$ID/$dir/, found $SEGMENTS" >&2
        echo "objects actually present:" >&2
        cat "$TMP/rendition-$dir.txt" >&2
        exit 1
    fi

    echo "    ${dir}p: playlist ${PLAYLIST_LEN}B, $SEGMENTS nonempty segments"
    TOTAL_SEGMENTS=$((TOTAL_SEGMENTS + SEGMENTS))
done

echo "PASS: video $ID reached ready with a ${MASTER_LEN}B master playlist listing exactly"
echo "      [$VARIANTS] in ascending bandwidth (no 1080p from a 720p source), $TOTAL_SEGMENTS nonempty"
echo "      segments across the ladder, a ${COVER_LEN}B cover and a ${SCRUB_LEN}B scrub thumbnail;"
echo "      the API serves it as $MASTER_URL, readable unsigned and cross-origin"
```

- [ ] **Step 3: Run the end-to-end check from a cold stack**

Run: `make down && make e2e`
Expected: PASS, with the per-rendition lines printed for `360p`, `480p`, and `720p`. If a rendition times out, `worker.log` is dumped by the existing `cleanup` trap — the JSON lines from Task 3 (`"msg":"rendition ladder"`, `"msg":"rendition encoded"`) show how far the ladder got.

- [ ] **Step 4: Update `README.md`**

Replace the status paragraph (line 5) with:

```markdown
**Status:** vertical slice implemented, with the full rendition ladder. `apps/api`, `apps/worker`, and `apps/web` run locally against LocalStack (S3 + SQS) and Postgres: a browser can upload a file, the worker probes it and transcodes every rendition the source resolution supports (1080p/720p/480p/360p, never upscaling), assembles a `master.m3u8` ordered by ascending bandwidth, and writes a cover image plus a strip of scrub thumbnails; the page plays it back. `scripts/e2e.sh` proves the pipeline end to end from a cold stack. Deletion, listing, CloudFront, and deployment to AWS remain unbuilt — architecture, infrastructure, and API contract for that fuller scope are specified and the Terraform module tree is implemented and `terraform validate`-clean.
```

and the worker line in the repository-layout block (line 89):

```
    worker/             SQS consumer: ffmpeg transcode to the 1080p/720p/480p/360p HLS ladder,
                        thumbnails, DB updates
```

- [ ] **Step 5: Update the two specs**

In `docs/specifications/vertical-slice-spec.md` §3, replace the ladder row:

```markdown
| 1080p / 480p / 360p renditions, source-resolution-aware selection | worker spec |
```

with:

```markdown
| ~~1080p / 480p / 360p renditions, source-resolution-aware selection~~ | delivered by [worker-rendition-ladder-plan.md](../plans/worker-rendition-ladder-plan.md) |
```

In `docs/specifications/ffmpeg-profiles.md` §5.3, fix the 360p `BANDWIDTH` so the example agrees with the rule stated directly beneath it (`maxrate` 850k + audio 96k = 946000; the other three lines already compute this way):

```
#EXT-X-STREAM-INF:BANDWIDTH=946000,RESOLUTION=640x360,CODECS="avc1.42001e,mp4a.40.2"
```

- [ ] **Step 6: Verify the whole repository is clean**

Run: `gofmt -l . && go vet ./... && go test ./... && grep -rn "720p HLS" README.md docs/specifications/vertical-slice-spec.md`
Expected: no gofmt output, no vet output, all tests PASS, and the `grep` finds nothing (exit 1) — every "720p only" claim is gone.

- [ ] **Step 7: Commit**

```bash
git add scripts/e2e.sh README.md docs/specifications/vertical-slice-spec.md docs/specifications/ffmpeg-profiles.md
git commit -m "docs: prove the rendition ladder end to end and retire the 720p-only status"
```
