#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test
export AWS_REGION=us-east-1
export AWS_ENDPOINT_URL=http://localhost:4566
export DATABASE_URL="postgres://user:userpassword@localhost:5432/videothing?sslmode=disable"
export RAW_BUCKET=video-thing-dev-raw-uploads
export PROCESSED_BUCKET=video-thing-dev-processed-assets
export PUBLIC_ASSET_BASE_URL="$AWS_ENDPOINT_URL/$PROCESSED_BUCKET"
export PORT=8080

for bin in docker jq ffmpeg aws go curl; do
    command -v "$bin" >/dev/null || { echo "missing required binary: $bin" >&2; exit 1; }
done

MIGRATE="${MIGRATE:-$(go env GOPATH)/bin/migrate}"
if [ ! -x "$MIGRATE" ]; then
    echo "migrate binary not found or not executable at $MIGRATE" >&2
    echo "install it with: go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest" >&2
    exit 1
fi

queue_depth() {
    aws --endpoint-url "$AWS_ENDPOINT_URL" sqs get-queue-attributes \
        --queue-url "$QUEUE_URL" \
        --attribute-names ApproximateNumberOfMessages ApproximateNumberOfMessagesNotVisible \
        --query 'Attributes.[ApproximateNumberOfMessages,ApproximateNumberOfMessagesNotVisible]' \
        --output text 2>/dev/null || true
}

require_nonempty_object() {
    local key="$1" label="$2" len
    len="$(aws --endpoint-url "$AWS_ENDPOINT_URL" s3api head-object \
        --bucket "$PROCESSED_BUCKET" --key "$key" --query ContentLength --output text 2>/dev/null || true)"
    if [ -z "$len" ] || [ "$len" = "None" ]; then
        echo "FAIL: $label is missing from $PROCESSED_BUCKET ($key)" >&2
        exit 1
    fi
    if [ "$len" -le 0 ]; then
        echo "FAIL: $label exists but is zero-length ($key)" >&2
        exit 1
    fi
    echo "$len"
}

TMP="$(mktemp -d)"
PIDS=()
cleanup() {
    local status=$?
    for pid in "${PIDS[@]:-}"; do kill "$pid" 2>/dev/null || true; done
    for pid in "${PIDS[@]:-}"; do wait "$pid" 2>/dev/null || true; done
    if [ "$status" -ne 0 ]; then
        echo "==> FAILURE (exit $status): dumping service logs" >&2
        if [ -f "$TMP/api.log" ]; then
            echo "--- api.log ---" >&2
            cat "$TMP/api.log" >&2
        fi
        if [ -f "$TMP/worker.log" ]; then
            echo "--- worker.log ---" >&2
            cat "$TMP/worker.log" >&2
        fi
    fi
    rm -rf "$TMP"
}
trap cleanup EXIT

echo "==> starting the local stack"
docker compose up -d

for attempt in $(seq 1 60); do
    if aws --endpoint-url "$AWS_ENDPOINT_URL" s3api head-bucket --bucket "$RAW_BUCKET" >/dev/null 2>&1 \
        && docker compose exec -T postgres pg_isready -U user >/dev/null 2>&1; then
        break
    fi
    [ "$attempt" -eq 60 ] && { echo "stack did not become ready" >&2; exit 1; }
    sleep 2
done

echo "==> resolving queue url"
QUEUE_URL=""
for attempt in $(seq 1 30); do
    QUEUE_URL="$(aws --endpoint-url "$AWS_ENDPOINT_URL" sqs get-queue-url \
        --queue-name video-thing-dev-video-processing --query QueueUrl --output text 2>/dev/null || true)"
    [ -n "$QUEUE_URL" ] && break
    [ "$attempt" -eq 30 ] && { echo "queue never became available" >&2; exit 1; }
    sleep 1
done
export QUEUE_URL

echo "==> waiting for bucket notification configuration"
for attempt in $(seq 1 30); do
    CONFIGURED="$(aws --endpoint-url "$AWS_ENDPOINT_URL" s3api get-bucket-notification-configuration \
        --bucket "$RAW_BUCKET" --query 'QueueConfigurations[0].QueueArn' --output text 2>/dev/null || true)"
    [ -n "$CONFIGURED" ] && [ "$CONFIGURED" != "None" ] && break
    [ "$attempt" -eq 30 ] && { echo "bucket notification configuration never appeared" >&2; exit 1; }
    sleep 1
done

echo "==> applying migrations"
"$MIGRATE" -path packages/database/migrations -database "$DATABASE_URL" up

echo "==> building and starting services"
go build -o "$TMP/api" ./apps/api
go build -o "$TMP/worker" ./apps/worker
"$TMP/api" >"$TMP/api.log" 2>&1 & PIDS+=($!)
"$TMP/worker" >"$TMP/worker.log" 2>&1 & WORKER_PID=$!; PIDS+=("$WORKER_PID")

for attempt in $(seq 1 30); do
    curl -sf "localhost:$PORT/healthz" >/dev/null && break
    [ "$attempt" -eq 30 ] && { echo "api did not start" >&2; exit 1; }
    sleep 1
done

echo "==> generating a 10s test clip"
ffmpeg -v error -y -f lavfi -i testsrc=size=1280x720:rate=30 -f lavfi -i sine=frequency=440 \
    -t 10 -c:v libx264 -pix_fmt yuv420p -c:a aac -shortest "$TMP/sample.mp4"

UPLOAD_ATTEMPTS=3
STATUS=""
for upload_attempt in $(seq 1 "$UPLOAD_ATTEMPTS"); do
    echo "==> uploading (attempt $upload_attempt/$UPLOAD_ATTEMPTS)"
    curl -sf -XPOST "localhost:$PORT/videos" -H 'content-type: application/json' \
        -d '{"title":"e2e"}' >"$TMP/created.json"
    ID="$(jq -r .video.id "$TMP/created.json")"
    curl -sf -XPUT "$(jq -r .upload.uploadUrl "$TMP/created.json")" \
        -H 'content-type: application/octet-stream' --data-binary "@$TMP/sample.mp4" -o /dev/null

    echo "==> waiting for processing (video $ID)"
    STATUS=""
    for attempt in $(seq 1 60); do
        STATUS="$(curl -sf "localhost:$PORT/videos/$ID" 2>/dev/null | jq -r .status 2>/dev/null || true)"
        [ "$STATUS" = "ready" ] && break
        if [ "$STATUS" = "failed" ]; then
            echo "FAIL: processing reported failed" >&2
            exit 1
        fi
        sleep 2
    done

    [ "$STATUS" = "ready" ] && break

    VISIBLE=""; INFLIGHT=""
    read -r VISIBLE INFLIGHT <<<"$(queue_depth)" || true
    VISIBLE="${VISIBLE:-unknown}"; INFLIGHT="${INFLIGHT:-unknown}"

    if grep -q "processing video $ID" "$TMP/worker.log" 2>/dev/null; then
        echo "FAIL: video $ID timed out with status=$STATUS, but the worker DID receive it" >&2
        echo "      (worker.log has a 'processing video $ID' line). Delivery worked and" >&2
        echo "      processing never completed, so this is a pipeline failure, not a" >&2
        echo "      delivery artifact. Refusing to retry -- investigate the worker." >&2
        exit 1
    fi

    if ! kill -0 "$WORKER_PID" 2>/dev/null; then
        echo "FAIL: the worker process ($WORKER_PID) is not running, which is why video $ID" >&2
        echo "      was never picked up. That is a worker startup/crash failure, not a" >&2
        echo "      delivery artifact. Refusing to retry." >&2
        exit 1
    fi

    if [ "$VISIBLE" != "0" ]; then
        echo "FAIL: video $ID timed out with status=$STATUS and $VISIBLE message(s) are sitting" >&2
        echo "      visible on the queue while the worker is alive and polling. The event was" >&2
        echo "      delivered and is not being consumed. Refusing to retry." >&2
        exit 1
    fi

    echo "!! SUSPECTED LOCALSTACK S3->SQS DELIVERY ARTIFACT for video $ID" >&2
    echo "!!   evidence: worker alive (pid $WORKER_PID), no receipt logged for this id," >&2
    echo "!!   queue visible=$VISIBLE in-flight=$INFLIGHT -- the job never reached the" >&2
    if [ "$INFLIGHT" != "0" ]; then
        echo "!!   application, and the message is stranded in-flight (LocalStack returned it" >&2
        echo "!!   from the queue without delivering it; it becomes visible again only after" >&2
        echo "!!   the 900s visibility timeout, well past this script's wait)." >&2
    else
        echo "!!   application, and no message exists at all (notification never published)." >&2
    fi
    echo "!!   This is a LocalStack artifact, not an API/worker defect." >&2
    if [ "$upload_attempt" -lt "$UPLOAD_ATTEMPTS" ]; then
        echo "!!   retrying with a fresh upload (attempt $((upload_attempt + 1))/$UPLOAD_ATTEMPTS)" >&2
    else
        echo "!!   no attempts left" >&2
    fi
done

if [ "$STATUS" != "ready" ]; then
    echo "FAIL: timed out with status=$STATUS after $UPLOAD_ATTEMPTS upload attempts" >&2
    exit 1
fi

echo "==> asserting processed assets"

MASTER_KEY="processed/$ID/master.m3u8"
MASTER_LEN="$(require_nonempty_object "$MASTER_KEY" "master playlist")"
aws --endpoint-url "$AWS_ENDPOINT_URL" s3api get-object \
    --bucket "$PROCESSED_BUCKET" --key "$MASTER_KEY" "$TMP/master.m3u8" >/dev/null
if ! head -n 1 "$TMP/master.m3u8" | grep -q '^#EXTM3U'; then
    echo "FAIL: master playlist does not start with #EXTM3U ($MASTER_KEY):" >&2
    head -n 5 "$TMP/master.m3u8" >&2
    exit 1
fi
if ! grep -q '720/playlist\.m3u8' "$TMP/master.m3u8"; then
    echo "FAIL: master playlist does not reference the 720p variant playlist ($MASTER_KEY):" >&2
    cat "$TMP/master.m3u8" >&2
    exit 1
fi

COVER_LEN="$(require_nonempty_object "processed/$ID/thumbnails/cover.jpg" "cover thumbnail")"

RENDITION_PLAYLIST_LEN="$(require_nonempty_object "processed/$ID/720/playlist.m3u8" "720p rendition playlist")"

aws --endpoint-url "$AWS_ENDPOINT_URL" s3api list-objects-v2 \
    --bucket "$PROCESSED_BUCKET" --prefix "processed/$ID/720/" \
    --query 'Contents[].[Key,Size]' --output text >"$TMP/rendition.txt"

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
done <"$TMP/rendition.txt"

if [ "$SEGMENTS" -lt 2 ]; then
    echo "FAIL: expected at least 2 segment_*.ts objects under processed/$ID/720/, found $SEGMENTS" >&2
    echo "objects actually present:" >&2
    cat "$TMP/rendition.txt" >&2
    exit 1
fi

echo "PASS: video $ID reached ready with a valid master playlist (${MASTER_LEN}B) referencing"
echo "      the 720p rendition (playlist ${RENDITION_PLAYLIST_LEN}B, $SEGMENTS nonempty segments)"
echo "      and a ${COVER_LEN}B cover thumbnail"
