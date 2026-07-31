# Task 1: Container images for the API and worker

> Task 1 of 9 in [`delivery`](00-context.md). Read [`00-context.md`](00-context.md) first — the goal, tech stack, Global Constraints, and file structure bind this task. Full plan: [`delivery-plan.md`](../../plans/delivery-plan.md).
>
> Next: [Task 2](task-02-migrations-as-explicit-pipeline-step.md)

---

**Files:**
- Create: `.dockerignore`
- Create: `docker/api.Dockerfile`
- Create: `docker/worker.Dockerfile`
- Create: `scripts/image-smoke.sh`
- Modify: `Makefile` (add `IMAGE_TAG`, `IMAGES`, `images`, `image-smoke`)

**Interfaces:**
- Consumes: `apps/api` and `apps/worker` as they stand; `apps/worker/main.go:27-31` requires `ffmpeg` and `ffprobe` on `PATH` at startup and `log.Fatalf`s otherwise; `apps/api/config.go` requires `DATABASE_URL`, `RAW_BUCKET`, `PUBLIC_ASSET_BASE_URL`.
- Produces: images `video-thing/api:local` and `video-thing/worker:local`, both running as uid 65532 with entrypoints `/usr/local/bin/api` and `/usr/local/bin/worker`. `scripts/image-smoke.sh` exits 0 only when both images satisfy the four invariants.

**Base image decision (one line each):**
- Runtime is `alpine:3.24.1` for both images because the worker shells out to `ffmpeg` and needs a real filesystem, `/tmp`, and a shell for `docker exec` troubleshooting — the things distroless removes are exactly the things a transcoder needs when something goes wrong.
- `ffmpeg`/`ffprobe` come from `mwader/static-ffmpeg:7.1.1` rather than `apk add ffmpeg` because apk resolves to whatever build is current in the Alpine branch, whereas a pinned upstream tag makes transcode output reproducible across rebuilds — and 7.1.1 is the conservative choice next to the host's 6.1.1 that the vertical slice was validated against.

**Version-pinning policy:** every `FROM` names an exact patch version, never a floating tag (`alpine:3.24.1`, not `alpine:3.24` or `alpine:latest`; `golang:1.25.12-alpine3.24`, not `golang:1.25-alpine`). Bumps are a deliberate commit that re-runs `scripts/image-smoke.sh` and `scripts/e2e.sh`. The Go builder patch version is bumped when a Go security release lands; the ffmpeg tag is bumped only with a `make e2e` run proving the ladder still produces playable output.

- [ ] **Step 1: Write `scripts/image-smoke.sh`**

```bash
#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

API_IMAGE="${API_IMAGE:-video-thing/api:local}"
WORKER_IMAGE="${WORKER_IMAGE:-video-thing/worker:local}"
FFMPEG_VERSION="${FFMPEG_VERSION:-7.1.1}"

fail() { echo "FAIL: $*" >&2; exit 1; }

echo "==> both images must run as uid 65532"
for image in "$API_IMAGE" "$WORKER_IMAGE"; do
    uid="$(docker run --rm --entrypoint id "$image" -u)"
    [ "$uid" = "65532" ] || fail "$image runs as uid $uid, expected 65532"
done

echo "==> both Go binaries must be statically linked"
for spec in "$API_IMAGE:/usr/local/bin/api" "$WORKER_IMAGE:/usr/local/bin/worker"; do
    image="${spec%:*}"
    binary="${spec##*:}"
    out="$(docker run --rm --entrypoint sh "$image" -c "ldd $binary 2>&1 || true")"
    case "$out" in
        *"not a dynamic executable"*|*"Not a valid dynamic program"*) ;;
        *) fail "$binary in $image is dynamically linked: $out" ;;
    esac
done

echo "==> the worker must carry ffmpeg and ffprobe $FFMPEG_VERSION"
for bin in ffmpeg ffprobe; do
    line="$(docker run --rm --entrypoint "$bin" "$WORKER_IMAGE" -version | head -1)"
    case "$line" in
        *"$FFMPEG_VERSION"*) ;;
        *) fail "worker $bin reports '$line', expected version $FFMPEG_VERSION" ;;
    esac
done

echo "==> the api image must answer /healthz"
CID="$(docker run -d --rm -p 18080:8080 \
    -e DATABASE_URL="postgres://user:userpassword@127.0.0.1:5432/videothing?sslmode=disable" \
    -e RAW_BUCKET=video-thing-dev-raw-uploads \
    -e PUBLIC_ASSET_BASE_URL=http://example.invalid \
    -e AWS_REGION=us-east-1 \
    "$API_IMAGE")"
trap 'docker rm -f "$CID" >/dev/null 2>&1 || true' EXIT

ok=""
for _ in $(seq 1 30); do
    if curl -fsS http://localhost:18080/healthz >/dev/null 2>&1; then ok=1; break; fi
    sleep 1
done
[ -n "$ok" ] || { docker logs "$CID" >&2 || true; fail "api image did not answer /healthz"; }

echo "PASS: $API_IMAGE and $WORKER_IMAGE satisfy the image invariants"
```

The API answers `/healthz` without a reachable database on purpose — `newRouter` wires `/healthz` to a constant and `/readyz` to `pool.Ping`, so this asserts the process boots and listens without needing Postgres in the smoke test.

- [ ] **Step 2: Run it and watch it fail**

```bash
chmod +x scripts/image-smoke.sh
./scripts/image-smoke.sh
```

Expected: FAIL, because the images do not exist yet. Docker prints `Unable to find image 'video-thing/api:local' locally` followed by `Error response from daemon: pull access denied for video-thing/api` and the script exits non-zero on the first `docker run`.

- [ ] **Step 3: Write `.dockerignore`**

Without this, `COPY apps ./apps` drags `apps/web/node_modules` and `apps/web/dist` into every build context, and `docker build` uploads hundreds of megabytes before doing anything.

```
.git
.github
.ai-jail
.claude
docs
infrastructure
scripts
compose.yml
Makefile
README.md
*.md
apps/web
/api
/worker
**/.terraform
**/*.tfstate
**/*.tfstate.*
**/*.tfvars
```

`docker/` is deliberately *not* ignored: `migrations.Dockerfile` (Task 2) copies nothing from it, but keeping the directory available avoids a surprise if a future image needs a helper file. `packages/` and `go.mod`/`go.sum` are not listed, so they stay in the context.

- [ ] **Step 4: Write `docker/api.Dockerfile`**

```dockerfile
FROM golang:1.25.12-alpine3.24 AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY apps ./apps
COPY packages ./packages

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o /out/api ./apps/api

FROM alpine:3.24.1

RUN apk add --no-cache ca-certificates \
    && addgroup -g 65532 -S app \
    && adduser -u 65532 -S -G app app

COPY --from=build /out/api /usr/local/bin/api

USER 65532:65532
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/api"]
```

`ca-certificates` is not optional: the API calls the S3 presign endpoint over HTTPS in AWS, and a scratch-alpine image without a trust store fails with `x509: certificate signed by unknown authority`.

- [ ] **Step 5: Write `docker/worker.Dockerfile`**

```dockerfile
FROM mwader/static-ffmpeg:7.1.1 AS ffmpeg

FROM golang:1.25.12-alpine3.24 AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY apps ./apps
COPY packages ./packages

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o /out/worker ./apps/worker

FROM alpine:3.24.1

RUN apk add --no-cache ca-certificates \
    && addgroup -g 65532 -S app \
    && adduser -u 65532 -S -G app app

COPY --from=ffmpeg /ffmpeg /usr/local/bin/ffmpeg
COPY --from=ffmpeg /ffprobe /usr/local/bin/ffprobe
COPY --from=build /out/worker /usr/local/bin/worker

USER 65532:65532

ENTRYPOINT ["/usr/local/bin/worker"]
```

- [ ] **Step 6: Add the image targets to the `Makefile`**

Insert after the `QUEUE_NAME ?= ...` line:

```makefile
IMAGE_TAG ?= local
IMAGES ?= api worker
```

Add `images image-smoke` to the `.PHONY` list, and append these targets at the end of the file:

```makefile
images:
	@for name in $(IMAGES); do \
		echo "==> building video-thing/$$name:$(IMAGE_TAG)"; \
		docker build --platform linux/amd64 -f docker/$$name.Dockerfile \
			-t video-thing/$$name:$(IMAGE_TAG) . || exit 1; \
	done

image-smoke: images
	./scripts/image-smoke.sh
```

`--platform linux/amd64` is pinned because the ECS task definitions declare `cpu_architecture = "X86_64"` (Task 3); on an arm64 laptop this builds under emulation and is slow but correct.

- [ ] **Step 7: Build both images and run the smoke test**

```bash
make image-smoke
```

Expected: three `==>` build lines, then `PASS: video-thing/api:local and video-thing/worker:local satisfy the image invariants`, exit code 0. Check the sizes are sane:

```bash
docker images --format '{{.Repository}}:{{.Tag}} {{.Size}}' | grep video-thing
```

Expected: the api image is roughly 20–30 MB and the worker roughly 130–180 MB (the static ffmpeg pair dominates).

- [ ] **Step 8: Drive one real video through the containerised services**

Start the compose stack and take note of its network name — compose derives it from the directory, so it is `video-thing_default`:

```bash
make up
docker network ls --format '{{.Name}}' | grep default
```

Run the API container on that network:

```bash
docker run -d --name vt-api --network video-thing_default -p 8080:8080 \
  -e DATABASE_URL="postgres://user:userpassword@postgres:5432/videothing?sslmode=disable" \
  -e AWS_ACCESS_KEY_ID=test -e AWS_SECRET_ACCESS_KEY=test -e AWS_REGION=us-east-1 \
  -e AWS_ENDPOINT_URL=http://localstack:4566 \
  -e RAW_BUCKET=video-thing-dev-raw-uploads \
  -e PUBLIC_ASSET_BASE_URL=http://localhost:4566/video-thing-dev-processed-assets \
  video-thing/api:local
```

The presigned URL the API mints will point at `http://localstack:4566/...`, which the host cannot resolve, so rewrite the host when uploading. Derive the queue URL and rewrite its host for the container too:

```bash
QUEUE_URL="$(aws --endpoint-url http://localhost:4566 sqs get-queue-url \
  --queue-name video-thing-dev-video-processing --query QueueUrl --output text \
  | sed 's#//[^/]*/#//localstack:4566/#')"
echo "$QUEUE_URL"

docker run -d --name vt-worker --network video-thing_default \
  -e DATABASE_URL="postgres://user:userpassword@postgres:5432/videothing?sslmode=disable" \
  -e AWS_ACCESS_KEY_ID=test -e AWS_SECRET_ACCESS_KEY=test -e AWS_REGION=us-east-1 \
  -e AWS_ENDPOINT_URL=http://localstack:4566 \
  -e QUEUE_URL="$QUEUE_URL" \
  -e PROCESSED_BUCKET=video-thing-dev-processed-assets \
  video-thing/worker:local
```

Generate a clip, upload it, and poll:

```bash
ffmpeg -v error -y -f lavfi -i testsrc=size=1280x720:rate=30 -f lavfi -i sine=frequency=440 \
  -t 10 -c:v libx264 -c:a aac -shortest /tmp/vt-sample.mp4

CREATED="$(curl -sf -XPOST localhost:8080/videos -H 'content-type: application/json' -d '{"title":"container run"}')"
ID="$(echo "$CREATED" | jq -r .video.id)"
URL="$(echo "$CREATED" | jq -r .upload.uploadUrl | sed 's#http://localstack:4566#http://localhost:4566#')"
curl -sf -XPUT "$URL" -H 'content-type: application/octet-stream' --data-binary @/tmp/vt-sample.mp4 -o /dev/null

for _ in $(seq 1 60); do
  STATUS="$(curl -sf "localhost:8080/videos/$ID" | jq -r .status)"
  echo "$STATUS"
  [ "$STATUS" = "ready" ] && break
  [ "$STATUS" = "failed" ] && { docker logs vt-worker; break; }
  sleep 2
done

aws --endpoint-url http://localhost:4566 s3 ls --recursive \
  "s3://video-thing-dev-processed-assets/processed/$ID/"
```

Expected: the status reaches `ready`, and the listing shows `master.m3u8`, `720/playlist.m3u8`, several `720/segment_*.ts`, and `thumbnails/cover.jpg`. If the worker logs `ffmpeg not found in PATH`, the `COPY --from=ffmpeg` paths are wrong — confirm with `docker run --rm --entrypoint ls video-thing/worker:local -l /usr/local/bin`.

Clean up:

```bash
docker rm -f vt-api vt-worker
```

- [ ] **Step 9: Run the full suite and commit**

```bash
gofmt -l .
go vet ./...
go test ./...
./scripts/image-smoke.sh
```

Expected: `gofmt -l .` prints nothing, vet and tests are clean, the smoke test PASSes.

```bash
git add .dockerignore docker/api.Dockerfile docker/worker.Dockerfile scripts/image-smoke.sh Makefile
git commit -m "feat: containerise the api and worker with pinned static builds"
```

---
