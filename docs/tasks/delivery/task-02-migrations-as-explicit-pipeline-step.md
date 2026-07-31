# Task 2: Migrations as an explicit pipeline step

> Task 2 of 9 in [`delivery`](00-context.md). Read [`00-context.md`](00-context.md) first — the goal, tech stack, Global Constraints, and file structure bind this task. Full plan: [`delivery-plan.md`](../../plans/delivery-plan.md).
>
> Previous: [Task 1](task-01-container-images-api-worker.md) · Next: [Task 3](task-03-ecs-task-definitions-pointing-at-real.md)

---

**Files:**
- Create: `docker/migrations.Dockerfile`
- Create: `scripts/run-migrations.sh`
- Create: `infrastructure/terraform/modules/migrations/main.tf`
- Create: `infrastructure/terraform/modules/migrations/variables.tf`
- Create: `infrastructure/terraform/modules/migrations/outputs.tf`
- Modify: `infrastructure/terraform/modules/rds/variables.tf` (append)
- Modify: `infrastructure/terraform/modules/rds/main.tf` (append)
- Modify: `infrastructure/terraform/modules/rds/outputs.tf` (append)
- Modify: `infrastructure/terraform/environments/dev/main.tf` (`ecr`, `logs`, `rds`, new `migrations` module)
- Modify: `infrastructure/terraform/environments/dev/variables.tf` (append `migrations_image_tag`)
- Modify: `infrastructure/terraform/environments/dev/outputs.tf` (append four outputs)
- Modify: `Makefile` (`IMAGES`, add `migrate-aws`)

**Why an ECS one-off task and not a step on the runner:** the RDS instance lives in private subnets with a security group that only admits the application security groups (`modules/rds/main.tf:25-53`). A GitHub-hosted runner has no route to it, and the alternatives — making the instance publicly accessible, or running a bastion/VPN in CI — either weaken the network boundary permanently or add a component to maintain. A Fargate task in the same private subnets already has the route, already gets its credentials from the task role, and already reads the password from Secrets Manager; `aws ecs run-task` plus `aws ecs wait tasks-stopped` plus one `describe-tasks` read of `containers[0].exitCode` gives the pipeline the exact same gate as a local `migrate` invocation.

**Interfaces:**
- Consumes: `packages/database/migrations/*.sql`; `module.ecr.repository_urls["migrations"]`; `module.networking.{vpc_id,private_subnet_ids}`; `module.logs.log_group_names["migrations"]`; `module.iam.ecs_task_execution_role_arn`.
- Produces: image `video-thing/migrations:local`; module `migrations` with outputs `task_definition_family`, `task_definition_arn`, `security_group_id`, `container_name`; `rds` output `database_url_secret_arn`; `scripts/run-migrations.sh` reading `CLUSTER`, `TASK_DEFINITION`, `SUBNETS`, `SECURITY_GROUPS` and optionally `CONTAINER_NAME`, `LOG_GROUP`, `STARTED_BY`.

- [ ] **Step 1: Write `docker/migrations.Dockerfile`**

```dockerfile
FROM golang:1.25.12-alpine3.24 AS build

RUN CGO_ENABLED=0 GOBIN=/out \
    go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@v4.19.1

FROM alpine:3.24.1

RUN apk add --no-cache ca-certificates \
    && addgroup -g 65532 -S app \
    && adduser -u 65532 -S -G app app

COPY --from=build /out/migrate /usr/local/bin/migrate
COPY packages/database/migrations /migrations

USER 65532:65532

# `sh -c` is required, not cosmetic: DATABASE_URL arrives as an ECS `secrets` entry, so the
# value only exists as an environment variable at container start and `migrate` has no
# env-var form of -database. `exec` keeps migrate as PID 1 so its exit code is the task's.
ENTRYPOINT ["/bin/sh", "-c"]
CMD ["exec migrate -path=/migrations -database=\"$DATABASE_URL\" up"]
```

`go install pkg@version` runs outside any module, so this does not add golang-migrate to `go.mod`. The `postgres` build tag is what `scripts/e2e.sh:24` already tells developers to use, so the container and the laptop run the same driver set.

- [ ] **Step 2: Build it and apply migrations against the compose Postgres**

```bash
make up
docker build --platform linux/amd64 -f docker/migrations.Dockerfile -t video-thing/migrations:local .
docker compose exec -T postgres psql -U user -d videothing -c 'drop table if exists videos; drop table if exists schema_migrations; drop type if exists video_status;'
docker run --rm --network video-thing_default \
  -e DATABASE_URL="postgres://user:userpassword@postgres:5432/videothing?sslmode=disable" \
  video-thing/migrations:local
echo "exit=$?"
docker compose exec -T postgres psql -U user -d videothing -c 'select version, dirty from schema_migrations;'
```

Expected: `exit=0`, and the query prints `version | dirty` with `1 | f`. Re-run the `docker run` line: expected `no change` on stderr and `exit=0` again — the step is idempotent, which matters because the pipeline may be re-run for the same SHA.

- [ ] **Step 3: Prove the image fails loudly on a bad database**

```bash
docker run --rm --network video-thing_default \
  -e DATABASE_URL="postgres://user:wrongpassword@postgres:5432/videothing?sslmode=disable" \
  video-thing/migrations:local
echo "exit=$?"
```

Expected: an authentication error on stderr and `exit=1`. This non-zero code is the whole basis of the pipeline gate, so confirm it now rather than in AWS.

- [ ] **Step 4: Add the assembled-DSN secret to the `rds` module**

Append to `infrastructure/terraform/modules/rds/variables.tf`:

```hcl
variable "skip_final_snapshot" {
  description = "Skip the final snapshot when the instance is destroyed. Must be false in production so a destroy still leaves a recoverable snapshot."
  type        = bool
  default     = true
}

variable "deletion_protection" {
  description = "Block deletion of the instance from both Terraform and the console. True in production."
  type        = bool
  default     = false
}
```

In `infrastructure/terraform/modules/rds/main.tf`, replace the hardcoded snapshot block (currently lines 79-81):

```hcl
  # Safe default for an MVP/dev environment so `terraform destroy` doesn't hang on a snapshot;
  # this MUST be false (and deletion_protection = true) once environment == "prod".
  skip_final_snapshot = true
```

with:

```hcl
  # The final snapshot identifier is fixed rather than timestamped: a timestamp would make
  # every plan show a diff. A second destroy after a real one therefore needs the previous
  # snapshot renamed or deleted first.
  skip_final_snapshot       = var.skip_final_snapshot
  final_snapshot_identifier = var.skip_final_snapshot ? null : "${var.project_name}-${var.environment}-db-final"
  deletion_protection       = var.deletion_protection
```

Then append to the same file:

```hcl
resource "aws_secretsmanager_secret" "database_url" {
  name = "${var.project_name}-${var.environment}-database-url"

  tags = {
    Name        = "${var.project_name}-${var.environment}-database-url"
    Project     = var.project_name
    Environment = var.environment
  }
}

# A second secret holding the assembled DSN rather than reusing the JSON one above: an ECS
# `secrets` entry injects one whole secret value into one environment variable, and both
# services read a single DATABASE_URL. sslmode=require encrypts the connection without
# pinning the RDS CA, which would mean baking the CA bundle into every image.
resource "aws_secretsmanager_secret_version" "database_url" {
  secret_id = aws_secretsmanager_secret.database_url.id
  secret_string = format(
    "postgres://%s:%s@%s:%d/%s?sslmode=require",
    var.db_username,
    urlencode(random_password.db.result),
    aws_db_instance.this.address,
    aws_db_instance.this.port,
    var.db_name,
  )
}
```

Append to `infrastructure/terraform/modules/rds/outputs.tf`:

```hcl
output "database_url_secret_arn" {
  description = "ARN of the Secrets Manager secret holding the full postgres:// DSN, injected into containers as DATABASE_URL."
  value       = aws_secretsmanager_secret.database_url.arn
}
```

- [ ] **Step 5: Write the `migrations` module**

`infrastructure/terraform/modules/migrations/variables.tf`:

```hcl
variable "project_name" {
  description = "Project name used for resource naming and tagging."
  type        = string
}

variable "environment" {
  description = "Deployment environment (e.g. dev, staging, production)."
  type        = string
}

variable "vpc_id" {
  description = "VPC in which to create the migration task security group."
  type        = string
}

variable "image" {
  description = "Fully-qualified image URI for the migrations container, e.g. ecr repository_urls[\"migrations\"] + \":sha\"."
  type        = string
}

variable "ecs_task_execution_role_arn" {
  description = "IAM role ECS uses to pull the image, read the DATABASE_URL secret, and write logs."
  type        = string
}

variable "database_url_secret_arn" {
  description = "ARN of the Secrets Manager secret holding the postgres:// DSN, injected as DATABASE_URL."
  type        = string
}

variable "log_group_name" {
  description = "CloudWatch log group name for the migration container."
  type        = string
}

variable "aws_region" {
  description = "AWS region, used in the awslogs log configuration."
  type        = string
}

variable "cpu" {
  description = "Fargate CPU units for the migration task; applying a handful of DDL statements needs no more than the minimum."
  type        = number
  default     = 256
}

variable "memory" {
  description = "Fargate memory (MiB) for the migration task."
  type        = number
  default     = 512
}
```

`infrastructure/terraform/modules/migrations/main.tf`:

```hcl
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

# Its own security group rather than reusing the API's: the RDS ingress list is the audit
# trail of what may reach the database, and a one-off admin task should be a distinct entry
# in it. Outbound only -- nothing ever connects to a migration task.
resource "aws_security_group" "migrations" {
  name        = "${var.project_name}-${var.environment}-migrations-sg"
  description = "One-off database migration tasks: no inbound, outbound only"
  vpc_id      = var.vpc_id

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = {
    Name        = "${var.project_name}-${var.environment}-migrations-sg"
    Project     = var.project_name
    Environment = var.environment
  }
}

# No task_role_arn: the container makes no AWS API calls of its own. The execution role is
# what reads the secret and ships the logs, and that belongs to the ECS agent, not the code.
resource "aws_ecs_task_definition" "migrations" {
  family                   = "${var.project_name}-${var.environment}-migrations"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = var.cpu
  memory                   = var.memory
  execution_role_arn       = var.ecs_task_execution_role_arn

  runtime_platform {
    operating_system_family = "LINUX"
    cpu_architecture        = "X86_64"
  }

  container_definitions = jsonencode([
    {
      name      = "migrations"
      image     = var.image
      essential = true
      secrets = [
        {
          name      = "DATABASE_URL"
          valueFrom = var.database_url_secret_arn
        }
      ]
      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = var.log_group_name
          "awslogs-region"        = var.aws_region
          "awslogs-stream-prefix" = "migrations"
        }
      }
    }
  ])

  tags = {
    Name        = "${var.project_name}-${var.environment}-migrations"
    Project     = var.project_name
    Environment = var.environment
  }
}
```

`infrastructure/terraform/modules/migrations/outputs.tf`:

```hcl
output "task_definition_family" {
  description = "Family name of the migration task definition; `aws ecs run-task --task-definition <family>` always picks the latest revision."
  value       = aws_ecs_task_definition.migrations.family
}

output "task_definition_arn" {
  description = "ARN of the current migration task definition revision."
  value       = aws_ecs_task_definition.migrations.arn
}

output "security_group_id" {
  description = "Security group attached to migration tasks; must appear in the RDS module's allowed_security_group_ids."
  value       = aws_security_group.migrations.id
}

output "container_name" {
  description = "Container name inside the task definition, needed to read the right exitCode out of describe-tasks."
  value       = "migrations"
}
```

- [ ] **Step 6: Wire the module into `environments/dev/main.tf`**

Four edits. First, pin the ECR repository list — the `web` app is a static bundle with no image, and `migrations` now needs one. Replace the `module "ecr"` block:

```hcl
module "ecr" {
  source = "../../modules/ecr"

  project_name = local.project_name
  environment  = local.environment
  # One repository per image this repo actually builds. `web` is a static Vite bundle with
  # no container, so it is not in the list.
  repository_names = ["api", "worker", "migrations"]
}
```

Second, give the migration container a log group. In `module "logs"`, replace:

```hcl
  log_group_names   = ["api", "worker"]
```

with:

```hcl
  log_group_names   = ["api", "worker", "migrations"]
```

Third, let migration tasks reach the database. In `module "rds"`, replace the `allowed_security_group_ids` block:

```hcl
  # API and worker task security groups both need DB access; these are
  # created by the ecs module, so this list is filled in after ecs exists.
  allowed_security_group_ids = [
    module.ecs.api_security_group_id,
    module.ecs.worker_security_group_id,
    module.migrations.security_group_id,
  ]
```

Fourth, add the module itself, immediately after `module "rds"`:

```hcl
module "migrations" {
  source = "../../modules/migrations"

  project_name = local.project_name
  environment  = local.environment
  vpc_id       = module.networking.vpc_id
  aws_region   = var.aws_region

  image                       = "${module.ecr.repository_urls["migrations"]}:${var.migrations_image_tag}"
  ecs_task_execution_role_arn = module.iam.ecs_task_execution_role_arn
  database_url_secret_arn     = module.rds.database_url_secret_arn
  log_group_name              = module.logs.log_group_names["migrations"]
}
```

- [ ] **Step 7: Add the variable and outputs the pipeline reads**

Append to `environments/dev/variables.tf`:

```hcl
variable "migrations_image_tag" {
  description = "Image tag to deploy for the one-off migrations task."
  type        = string
  default     = "latest"
}
```

Append to `environments/dev/outputs.tf`:

```hcl
output "private_subnet_ids" {
  description = "Private subnet IDs, needed by `aws ecs run-task` for the migration task."
  value       = module.networking.private_subnet_ids
}

output "migrations_task_definition_family" {
  description = "Family of the migration task definition."
  value       = module.migrations.task_definition_family
}

output "migrations_security_group_id" {
  description = "Security group to attach to the migration task."
  value       = module.migrations.security_group_id
}

output "migrations_log_group_name" {
  description = "CloudWatch log group the migration task writes to."
  value       = module.logs.log_group_names["migrations"]
}
```

- [ ] **Step 8: Validate the Terraform**

```bash
cd infrastructure/terraform
terraform fmt -check -recursive
cd environments/dev
terraform init -backend=false
terraform validate
cd ../../../..
```

Expected: `fmt -check` prints nothing, `validate` prints `Success! The configuration is valid.` If `fmt -check` names a file, run `terraform fmt -recursive` from `infrastructure/terraform` and re-check.

- [ ] **Step 9: Write `scripts/run-migrations.sh`**

```bash
#!/usr/bin/env bash
set -euo pipefail

CLUSTER="${CLUSTER:?CLUSTER is required (ECS cluster name)}"
TASK_DEFINITION="${TASK_DEFINITION:?TASK_DEFINITION is required (family, or family:revision)}"
SUBNETS="${SUBNETS:?SUBNETS is required (comma-separated private subnet ids)}"
SECURITY_GROUPS="${SECURITY_GROUPS:?SECURITY_GROUPS is required (comma-separated security group ids)}"
CONTAINER_NAME="${CONTAINER_NAME:-migrations}"
STARTED_BY="${STARTED_BY:-run-migrations.sh}"
LOG_GROUP="${LOG_GROUP:-}"

echo "==> running $TASK_DEFINITION on cluster $CLUSTER"

RUN_OUT="$(aws ecs run-task \
    --cluster "$CLUSTER" \
    --task-definition "$TASK_DEFINITION" \
    --launch-type FARGATE \
    --count 1 \
    --started-by "$STARTED_BY" \
    --network-configuration "awsvpcConfiguration={subnets=[$SUBNETS],securityGroups=[$SECURITY_GROUPS],assignPublicIp=DISABLED}" \
    --output json)"

TASK_ARN="$(printf '%s' "$RUN_OUT" | jq -r '.tasks[0].taskArn // empty')"
if [ -z "$TASK_ARN" ]; then
    echo "FAIL: run-task started no task" >&2
    printf '%s\n' "$RUN_OUT" >&2
    exit 1
fi
echo "==> task $TASK_ARN, waiting for it to stop"

aws ecs wait tasks-stopped --cluster "$CLUSTER" --tasks "$TASK_ARN"

DESCRIBE="$(aws ecs describe-tasks --cluster "$CLUSTER" --tasks "$TASK_ARN" --output json)"
EXIT_CODE="$(printf '%s' "$DESCRIBE" \
    | jq -r --arg c "$CONTAINER_NAME" '.tasks[0].containers[] | select(.name == $c) | .exitCode // "null"')"
STOP_CODE="$(printf '%s' "$DESCRIBE" | jq -r '.tasks[0].stopCode // "unknown"')"
STOPPED_REASON="$(printf '%s' "$DESCRIBE" | jq -r '.tasks[0].stoppedReason // "unknown"')"

echo "==> exitCode=$EXIT_CODE stopCode=$STOP_CODE reason=$STOPPED_REASON"

if [ -n "$LOG_GROUP" ]; then
    echo "==> migration log"
    aws logs tail "$LOG_GROUP" --since 15m || true
fi

if [ "$EXIT_CODE" != "0" ]; then
    echo "FAIL: migration task did not exit 0 (exitCode=$EXIT_CODE, stopCode=$STOP_CODE, reason=$STOPPED_REASON)" >&2
    exit 1
fi

echo "OK: migrations applied"
```

An `exitCode` of `null` — the container never started, for example because the image tag does not exist or the execution role cannot read the secret — is not `0`, so it fails. That is deliberate: a migration step that cannot prove it ran must not let the deploy continue.

- [ ] **Step 10: Add the Makefile targets**

Change the `IMAGES` line added in Task 1 to:

```makefile
IMAGES ?= api worker migrations
```

Add `migrate-aws` to `.PHONY` and append:

```makefile
ENV ?= dev

migrate-aws:
	@cd infrastructure/terraform/environments/$(ENV) && \
		CLUSTER="$$(terraform output -raw ecs_cluster_name)" \
		TASK_DEFINITION="$$(terraform output -raw migrations_task_definition_family)" \
		SUBNETS="$$(terraform output -json private_subnet_ids | jq -r 'join(",")')" \
		SECURITY_GROUPS="$$(terraform output -raw migrations_security_group_id)" \
		LOG_GROUP="$$(terraform output -raw migrations_log_group_name)" \
		$(CURDIR)/scripts/run-migrations.sh
```

Then rebuild all three images to confirm the loop picked up the new name:

```bash
chmod +x scripts/run-migrations.sh
make images
docker images --format '{{.Repository}}:{{.Tag}}' | grep video-thing
```

Expected: `video-thing/api:local`, `video-thing/worker:local`, `video-thing/migrations:local`.

- [ ] **Step 11: [AWS ONLY] Run it against a real environment**

This cannot be exercised without an AWS account, and it costs money (see "What costs money while idle"). Once Task 8 has applied `environments/dev` at least once:

```bash
make migrate-aws ENV=dev
```

Expected output ends with `==> exitCode=0 stopCode=EssentialContainerExited ...`, the tailed log line `1/u create_videos_table (...)` or `no change`, and `OK: migrations applied`. Confirm the schema landed:

```bash
aws logs tail /ecs/video-thing-dev-migrations --since 10m
```

If `exitCode=null` with `reason` mentioning `ResourceInitializationError: unable to pull secrets`, the execution role is missing the Secrets Manager grant — that is Task 3 step 3.

- [ ] **Step 12: Commit**

```bash
git add docker/migrations.Dockerfile scripts/run-migrations.sh Makefile \
  infrastructure/terraform/modules/migrations infrastructure/terraform/modules/rds \
  infrastructure/terraform/environments/dev
git commit -m "feat: run migrations as a gated one-off ECS task"
```

---
