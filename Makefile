# Defaults for the compose stack (LocalStack + Postgres). Exported so the
# run targets below need no per-invocation environment. Override any of them
# on the command line: `make api PORT=9090`.
export DATABASE_URL ?= postgres://user:userpassword@localhost:5432/videothing?sslmode=disable
export AWS_ACCESS_KEY_ID ?= test
export AWS_SECRET_ACCESS_KEY ?= test
export AWS_REGION ?= us-east-1
export AWS_ENDPOINT_URL ?= http://localhost:4566
export RAW_BUCKET ?= video-thing-dev-raw-uploads
export PROCESSED_BUCKET ?= video-thing-dev-processed-assets
export PUBLIC_ASSET_BASE_URL ?= $(AWS_ENDPOINT_URL)/$(PROCESSED_BUCKET)
export PORT ?= 8080

QUEUE_NAME ?= video-thing-dev-video-processing

.PHONY: up down migrate-up migrate-down sqlc test api worker web e2e

up:
	docker compose up -d
	@until docker compose exec -T postgres pg_isready -U user -d videothing >/dev/null 2>&1; do sleep 1; done
	@until aws --endpoint-url "$(AWS_ENDPOINT_URL)" sqs get-queue-url --queue-name "$(QUEUE_NAME)" >/dev/null 2>&1; do sleep 1; done
	$(MAKE) migrate-up

down:
	docker compose down

api:
	go run ./apps/api

# The queue URL is derived rather than hardcoded: LocalStack's SQS URL host
# depends on its version and configuration.
worker:
	QUEUE_URL="$$(aws --endpoint-url "$(AWS_ENDPOINT_URL)" sqs get-queue-url \
		--queue-name "$(QUEUE_NAME)" --query QueueUrl --output text)" go run ./apps/worker

web:
	cd apps/web && pnpm install && pnpm dev

e2e:
	./scripts/e2e.sh

migrate-up:
	migrate -path packages/database/migrations -database "$(DATABASE_URL)" up

migrate-down:
	migrate -path packages/database/migrations -database "$(DATABASE_URL)" down 1

sqlc:
	cd packages/database && sqlc generate

test:
	go test ./...
