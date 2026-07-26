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
"$TMP/worker" >"$TMP/worker.log" 2>&1 & PIDS+=($!)

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
        STATUS="$(curl -sf "localhost:$PORT/videos/$ID" | jq -r .status)"
        [ "$STATUS" = "ready" ] && break
        if [ "$STATUS" = "failed" ]; then
            echo "FAIL: processing reported failed" >&2
            exit 1
        fi
        sleep 2
    done

    [ "$STATUS" = "ready" ] && break
    echo "video $ID timed out with status=$STATUS (possible LocalStack notification loss); $([ "$upload_attempt" -lt "$UPLOAD_ATTEMPTS" ] && echo "retrying with a new upload" || echo "no attempts left")" >&2
done

if [ "$STATUS" != "ready" ]; then
    echo "FAIL: timed out with status=$STATUS after $UPLOAD_ATTEMPTS upload attempts" >&2
    exit 1
fi

echo "==> asserting processed assets"
aws --endpoint-url "$AWS_ENDPOINT_URL" s3api head-object \
    --bucket "$PROCESSED_BUCKET" --key "processed/$ID/master.m3u8" >/dev/null
aws --endpoint-url "$AWS_ENDPOINT_URL" s3api head-object \
    --bucket "$PROCESSED_BUCKET" --key "processed/$ID/thumbnails/cover.jpg" >/dev/null

SEGMENTS="$(aws --endpoint-url "$AWS_ENDPOINT_URL" s3api list-objects-v2 \
    --bucket "$PROCESSED_BUCKET" --prefix "processed/$ID/720/" \
    --query 'length(Contents)' --output text)"
[ "$SEGMENTS" -ge 2 ] || { echo "FAIL: only $SEGMENTS objects under 720/" >&2; exit 1; }

echo "PASS: video $ID reached ready with $SEGMENTS objects in the 720p rendition"
