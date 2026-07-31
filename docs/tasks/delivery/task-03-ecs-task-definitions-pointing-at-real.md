# Task 3: ECS task definitions pointing at real images, with the environment the services read

> Task 3 of 9 in [`delivery`](00-context.md). Read [`00-context.md`](00-context.md) first — the goal, tech stack, Global Constraints, and file structure bind this task. Full plan: [`delivery-plan.md`](../../plans/delivery-plan.md).
>
> Previous: [Task 2](task-02-migrations-as-explicit-pipeline-step.md) · Next: [Task 4](task-04-dlq-redrive-policy-alarms-make-it.md)

---

**Files:**
- Modify: `infrastructure/terraform/modules/ecs/variables.tf` (append three variables)
- Modify: `infrastructure/terraform/modules/ecs/main.tf` (both task definitions, both services)
- Modify: `infrastructure/terraform/modules/iam/main.tf` (execution role secrets read; API role S3 delete/list)
- Modify: `infrastructure/terraform/modules/networking/main.tf` (append the S3 gateway endpoint)
- Modify: `infrastructure/terraform/modules/networking/outputs.tf` (append the endpoint id)
- Modify: `infrastructure/terraform/environments/dev/main.tf` (`ecs` block env/secrets/sizes, `depends_on`)

**What is wrong today:** `environments/dev/main.tf:170-181` passes `DATABASE_SECRET_ARN` and `CDN_DOMAIN` as plain environment variables. Neither name exists in `apps/api/config.go` or `apps/worker/config.go`, so both services would `log.Fatalf("config: missing required environment variables: DATABASE_URL, PUBLIC_ASSET_BASE_URL")` on their first boot — and a secret ARN in an `environment` entry is not a secret being read, it is a string the container would have to resolve itself. The task definitions also declare no `runtime_platform`, so a Fargate launch would default the architecture independently of what the images were built for.

**Task sizes: the floor, and the one exception.** Fargate only accepts fixed CPU/memory pairs, so "the floor" means the smallest valid pair that runs the workload.

- **API — 256 CPU (0.25 vCPU) / 512 MB, `desired_count = 1`.** That is the smallest pair Fargate offers. A Gin router with a `pgxpool` and an S3 presign client is idle almost all the time and holds nothing in memory; presigning is a local HMAC, not a network call. One task means no HA, which is the right trade for a demo — and because the service keeps `deployment_minimum_healthy_percent = 100` with a 200% maximum, ECS still starts the replacement task before draining the old one, so deploys are not a visible outage.
- **Worker — 1024 CPU (1 vCPU) / 2048 MB. This is the exception, and it is deliberate.** The rendition-ladder plan runs *one ffmpeg process per eligible rendition, sequentially* (never `-var_stream_map`, per `ffmpeg-profiles.md` §5.3), so a 1080p source means four consecutive x264 encodes on the same task. x264 throughput is very close to linear in vCPU, and 1 vCPU puts a 1080p pass at roughly real time — a 60-second clip finishes its four passes in a handful of minutes, comfortably inside the 900-second visibility timeout Task 4 sets. At 512 CPU (0.5 vCPU) the same job takes twice as long and a modest clip starts running past that timeout, which is precisely the mid-flight-redelivery failure Task 4 exists to remove; 256 CPU is not viable at all. So the worker is sized by the ladder's wall-clock budget, not by the price list. It still costs nothing at rest: `worker_min_count = 0` means no worker task exists until a message arrives.
- **Ephemeral storage — 21 GiB**, the smallest value that can be set explicitly. Fargate includes 20 GiB free and bills $0.000111/GB-hour beyond it, only while a task runs, so the twenty-first gigabyte costs about eight hundredths of a cent per transcoding hour. A source large enough to exhaust 21 GiB across four renditions fails with `No space left on device`, which the rendition-ladder plan classifies as *retryable* — so the symptom is a redelivery, not a corrupt row, and raising this variable is the fix.

**Interfaces:**
- Consumes: `apps/api/config.go` `LoadConfig` — reads `DATABASE_URL`, `RAW_BUCKET`, `AWS_ENDPOINT_URL`, `PUBLIC_ASSET_BASE_URL`, `PORT`; requires the first, second, and fourth to be non-empty. `apps/worker/config.go` `LoadConfig` — reads `DATABASE_URL`, `QUEUE_URL`, `PROCESSED_BUCKET`, `AWS_ENDPOINT_URL`; requires the first three.
- Produces: `ecs` module variables `api_secrets`, `worker_secrets`, `worker_ephemeral_storage_gib`; task definitions whose containers carry `DATABASE_URL` as an ECS `secrets` entry and no `AWS_ENDPOINT_URL` at all; `networking` output `s3_vpc_endpoint_id`.

**Which variable goes where, and why:**

| Variable | API | Worker | Source |
|---|---|---|---|
| `DATABASE_URL` | yes | yes | ECS `secrets` → `module.rds.database_url_secret_arn` |
| `RAW_BUCKET` | yes | no | `apps/worker` takes the bucket off the S3 event (`event.go`), it never reads `RAW_BUCKET` |
| `PROCESSED_BUCKET` | yes | yes | the worker writes it; the API needs it for the `DELETE /videos/{id}` cleanup (contract 3) |
| `QUEUE_URL` | no | yes | already injected by the `ecs` module (`main.tf:155-158`) |
| `PUBLIC_ASSET_BASE_URL` | yes | no | the CloudFront domain; only the API builds URLs (contract 4) |
| `PORT` | yes | no | matches `api_container_port` so the ALB target group and the listener agree |
| `AWS_REGION` | yes | yes | **not** injected by Fargate; without it the AWS SDK cannot resolve a region and every S3/SQS call fails at signing time |
| `AWS_ENDPOINT_URL` | **absent** | **absent** | both `main.go` files treat empty as "use the real AWS endpoints"; setting it in AWS would send traffic to LocalStack |

- [ ] **Step 1: Add the new `ecs` module variables**

Append to `infrastructure/terraform/modules/ecs/variables.tf`:

```hcl
variable "api_secrets" {
  description = "Secrets Manager (or SSM) ARNs injected into the API container as environment variables, keyed by variable name. Read by the ECS agent using the task execution role, so the value never appears in the task definition."
  type        = map(string)
  default     = {}
}

variable "worker_secrets" {
  description = "Secrets Manager (or SSM) ARNs injected into the worker container as environment variables, keyed by variable name."
  type        = map(string)
  default     = {}
}

variable "worker_ephemeral_storage_gib" {
  description = "Fargate ephemeral storage for the worker task; ffmpeg writes the downloaded source plus every output segment to disk. 21 is the smallest value that can be set explicitly (20 GiB is included free, and beyond that Fargate bills $0.000111/GB-hour only while a task runs). Raise it if the ladder starts failing with 'No space left on device'. Maximum 200."
  type        = number
  default     = 21
}
```

- [ ] **Step 2: Rewrite both task definitions and both services**

In `infrastructure/terraform/modules/ecs/main.tf`, replace `resource "aws_ecs_task_definition" "api"` (lines 76-113) with:

```hcl
resource "aws_ecs_task_definition" "api" {
  family                   = "${var.project_name}-${var.environment}-api"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = var.api_cpu
  memory                   = var.api_memory
  execution_role_arn       = var.ecs_task_execution_role_arn
  task_role_arn            = var.api_task_role_arn

  runtime_platform {
    operating_system_family = "LINUX"
    cpu_architecture        = "X86_64"
  }

  container_definitions = jsonencode([
    {
      name      = "api"
      image     = var.api_image
      essential = true
      portMappings = [
        {
          containerPort = var.api_container_port
          protocol      = "tcp"
        }
      ]
      environment = [for k, v in var.api_env_vars : { name = k, value = v }]
      secrets     = [for k, v in var.api_secrets : { name = k, valueFrom = v }]
      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = var.api_log_group_name
          "awslogs-region"        = var.aws_region
          "awslogs-stream-prefix" = "api"
        }
      }
    }
  ])

  tags = {
    Name        = "${var.project_name}-${var.environment}-api"
    Project     = var.project_name
    Environment = var.environment
  }
}
```

Replace `resource "aws_ecs_service" "api"` (lines 115-139) with:

```hcl
resource "aws_ecs_service" "api" {
  name            = "${var.project_name}-${var.environment}-api"
  cluster         = aws_ecs_cluster.this.id
  task_definition = aws_ecs_task_definition.api.arn
  launch_type     = "FARGATE"
  desired_count   = var.api_desired_count

  deployment_minimum_healthy_percent = 100
  deployment_maximum_percent         = 200
  health_check_grace_period_seconds  = 60

  # Without the circuit breaker a task that crash-loops on a bad image or a missing secret
  # leaves the deployment "in progress" until `aws ecs wait services-stable` times out ten
  # minutes later with no explanation. With it, ECS rolls back to the previous task
  # definition and the pipeline's wait fails fast against a service that is still serving.
  deployment_circuit_breaker {
    enable   = true
    rollback = true
  }

  network_configuration {
    subnets          = var.private_subnet_ids
    security_groups  = [aws_security_group.api.id]
    assign_public_ip = false
  }

  load_balancer {
    target_group_arn = var.alb_target_group_arn
    container_name   = "api"
    container_port   = var.api_container_port
  }

  tags = {
    Name        = "${var.project_name}-${var.environment}-api"
    Project     = var.project_name
    Environment = var.environment
  }
}
```

Replace `resource "aws_ecs_task_definition" "worker"` (lines 141-175) with:

```hcl
resource "aws_ecs_task_definition" "worker" {
  family                   = "${var.project_name}-${var.environment}-worker"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = var.worker_cpu
  memory                   = var.worker_memory
  execution_role_arn       = var.ecs_task_execution_role_arn
  task_role_arn            = var.worker_task_role_arn

  runtime_platform {
    operating_system_family = "LINUX"
    cpu_architecture        = "X86_64"
  }

  ephemeral_storage {
    size_in_gib = var.worker_ephemeral_storage_gib
  }

  container_definitions = jsonencode([
    {
      name      = "worker"
      image     = var.worker_image
      essential = true
      environment = concat(
        [for k, v in var.worker_env_vars : { name = k, value = v }],
        [{ name = "QUEUE_URL", value = var.sqs_queue_url }]
      )
      secrets = [for k, v in var.worker_secrets : { name = k, valueFrom = v }]
      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = var.worker_log_group_name
          "awslogs-region"        = var.aws_region
          "awslogs-stream-prefix" = "worker"
        }
      }
    }
  ])

  tags = {
    Name        = "${var.project_name}-${var.environment}-worker"
    Project     = var.project_name
    Environment = var.environment
  }
}
```

In `resource "aws_ecs_service" "worker"`, add the two deployment percentages immediately after the `desired_count` line:

```hcl
  # Zero minimum healthy percent: worker_min_count is 0 for scale-to-zero, and a service
  # whose desired count can legitimately be 0 never reaches "steady state" if ECS is told to
  # keep 100% of tasks running through a deployment.
  deployment_minimum_healthy_percent = 0
  deployment_maximum_percent         = 200
```

- [ ] **Step 3: Grant the execution role the secret and the API role the deletes**

In `infrastructure/terraform/modules/iam/main.tf`, append a statement to `data "aws_iam_policy_document" "ecs_task_execution_extra"` (inside the existing block, after the `CloudWatchLogsWrite` statement):

```hcl
  statement {
    sid    = "SecretsRead"
    effect = "Allow"
    actions = [
      "secretsmanager:GetSecretValue",
    ]
    # Scoped by name prefix, not by exact ARN. Secrets Manager appends a random suffix to
    # every secret ARN, and taking the ARNs as an input would make this module depend on the
    # rds module -- which depends, through the RDS security group, on the ecs module's
    # security groups. The prefix keeps the graph acyclic and matches the scoping convention
    # in docs/specifications/github-oidc-role.md.
    resources = ["arn:aws:secretsmanager:*:*:secret:${var.project_name}-${var.environment}-*"]
  }
```

Append three statements to `data "aws_iam_policy_document" "api_task_policy"`, after the existing `ProcessedBucketObjectRead` statement:

```hcl
  # DELETE /videos/{id} removes the single raw object and everything under
  # processed/{id}/ (cross-plan contract 3), which needs ListObjectsV2 on the bucket and
  # DeleteObject on the keys. Granted here because IAM is this plan's responsibility even
  # though the handler ships with the api plan.
  statement {
    sid    = "RawBucketObjectDelete"
    effect = "Allow"
    actions = [
      "s3:DeleteObject",
    ]
    resources = ["${var.s3_raw_bucket_arn}/*"]
  }

  statement {
    sid    = "ProcessedBucketObjectDelete"
    effect = "Allow"
    actions = [
      "s3:DeleteObject",
    ]
    resources = ["${var.s3_processed_bucket_arn}/*"]
  }

  statement {
    sid    = "ProcessedBucketList"
    effect = "Allow"
    actions = [
      "s3:ListBucket",
    ]
    # Bucket-level action: the bare bucket ARN, no /* suffix.
    resources = [var.s3_processed_bucket_arn]
  }
```

- [ ] **Step 4: Route S3 traffic off the NAT gateway with a free gateway endpoint**

Append to `infrastructure/terraform/modules/networking/main.tf`:

```hcl
# A *gateway* endpoint, which unlike an interface endpoint has no hourly charge. It adds a
# more specific route for S3's prefix list to every private route table, so the worker's
# source downloads and segment uploads -- and ECR image layers, which S3 serves -- stop
# crossing the NAT gateway and stop paying its $0.045/GB data processing charge.
resource "aws_vpc_endpoint" "s3" {
  vpc_id            = aws_vpc.this.id
  service_name      = "com.amazonaws.${data.aws_region.current.name}.s3"
  vpc_endpoint_type = "Gateway"
  route_table_ids   = aws_route_table.private[*].id

  tags = {
    Name        = "${var.project_name}-${var.environment}-s3-endpoint"
    Project     = var.project_name
    Environment = var.environment
  }
}
```

and add the region lookup at the top of the same file, immediately after the `terraform` block:

```hcl
data "aws_region" "current" {}
```

Append to `infrastructure/terraform/modules/networking/outputs.tf`:

```hcl
output "s3_vpc_endpoint_id" {
  description = "ID of the S3 gateway VPC endpoint. Gateway endpoints are free; this exists so S3 and ECR-layer traffic does not cross the NAT gateway."
  value       = aws_vpc_endpoint.s3.id
}
```

No environment wiring is needed — the module creates it unconditionally because it is free in every environment and there is no case for turning it off.

- [ ] **Step 5: Rewire the `ecs` block in `environments/dev/main.tf`**

Replace the `api_env_vars` and `worker_env_vars` blocks (lines 170-181) with:

```hcl
  # AWS_ENDPOINT_URL is deliberately absent. Both services treat an empty value as "use the
  # real AWS endpoints" (apps/api/main.go, apps/worker/main.go); setting it here would point
  # production traffic at a LocalStack address. AWS_REGION, by contrast, must be explicit --
  # Fargate does not inject it, and the AWS SDK cannot sign a request without a region.
  api_env_vars = {
    AWS_REGION            = var.aws_region
    RAW_BUCKET            = module.s3.raw_bucket_id
    PROCESSED_BUCKET      = module.s3.processed_bucket_id
    PUBLIC_ASSET_BASE_URL = "https://${module.cloudfront.distribution_domain_name}"
    PORT                  = tostring(var.api_container_port)
  }

  api_secrets = {
    DATABASE_URL = module.rds.database_url_secret_arn
  }

  worker_env_vars = {
    AWS_REGION       = var.aws_region
    PROCESSED_BUCKET = module.s3.processed_bucket_id
  }

  worker_secrets = {
    DATABASE_URL = module.rds.database_url_secret_arn
  }
```

Add a `depends_on` as the last argument of the same `module "ecs"` block, after the closing brace of `worker_secrets`:

```hcl
  # The task execution role's inline policy (image pull, log write, secret read) must exist
  # before a service tries to start a task, or the first launch attempt fails with
  # ResourceInitializationError. The iam module has no dependency on ecs, so this is safe.
  depends_on = [module.iam]
```

Then add the four size arguments to the same block, next to `api_desired_count`. The `ecs` module's own defaults are 512/1024 for the API and 1024/2048 for the worker; the API must be taken down to the Fargate floor, and the worker's value must be stated rather than inherited so the reasoning above is visible at the call site:

```hcl
  api_cpu       = var.api_cpu
  api_memory    = var.api_memory
  worker_cpu    = var.worker_cpu
  worker_memory = var.worker_memory
```

Those four variables are declared in Task 7 alongside the other per-environment sizes. Until then `terraform validate` will report `Reference to undeclared input variable`, so declare them now in `environments/dev/variables.tf` with the floor defaults and let Task 7 copy them into the other two environments:

```hcl
variable "api_cpu" {
  description = "Fargate CPU units for the API task. 256 is the Fargate floor and is ample for a Gin router with a connection pool."
  type        = number
  default     = 256
}

variable "api_memory" {
  description = "Fargate memory (MiB) for the API task. 512 is the smallest value valid with 256 CPU units."
  type        = number
  default     = 512
}

variable "worker_cpu" {
  description = "Fargate CPU units for the worker task. 1024 (1 vCPU), not the 256 floor: the ladder runs four sequential x264 encodes and at 0.5 vCPU a modest clip outruns the 900s visibility timeout."
  type        = number
  default     = 1024
}

variable "worker_memory" {
  description = "Fargate memory (MiB) for the worker task. 2048 is the smallest value valid with 1024 CPU units."
  type        = number
  default     = 2048
}
```

- [ ] **Step 6: Validate**

```bash
cd infrastructure/terraform
terraform fmt -check -recursive
cd environments/dev
terraform init -backend=false
terraform validate
cd ../../../..
```

Expected: `Success! The configuration is valid.` If `validate` reports `Cycle:`, the `depends_on` was placed on the wrong module — it belongs on `module "ecs"`, never on `module "iam"` or `module "rds"`.

- [ ] **Step 7: Confirm the rendered container definitions by hand**

Terraform cannot render `jsonencode` output without a plan, so assert the shape from the module source instead:

```bash
grep -n 'secrets\|runtime_platform\|ephemeral_storage\|AWS_ENDPOINT_URL' \
  infrastructure/terraform/modules/ecs/main.tf \
  infrastructure/terraform/environments/dev/main.tf
```

Expected: `secrets` appears once in each task definition, `runtime_platform` twice, `ephemeral_storage` once, and `AWS_ENDPOINT_URL` appears **only** inside the explanatory comment in `environments/dev/main.tf` — never as a map key.

- [ ] **Step 8: Re-run the local suite**

```bash
gofmt -l .
go vet ./...
go test ./...
```

Expected: clean. No Go file changed in this task; this is the task-boundary check.

- [ ] **Step 9: [AWS ONLY] Plan against a real account**

```bash
cd infrastructure/terraform/environments/dev
terraform init
terraform plan -var "api_image_tag=$(git rev-parse HEAD)" \
  -var "worker_image_tag=$(git rev-parse HEAD)" \
  -var "migrations_image_tag=$(git rev-parse HEAD)"
```

Expected: the plan output for `module.ecs.aws_ecs_task_definition.api` shows a `container_definitions` JSON string containing `"secrets":[{"name":"DATABASE_URL","valueFrom":"arn:aws:secretsmanager:..."}]`, an `environment` array with `AWS_REGION`, `PORT`, `PROCESSED_BUCKET`, `PUBLIC_ASSET_BASE_URL`, `RAW_BUCKET` and nothing else, and `"cpuArchitecture":"X86_64"`. Do **not** apply here — Task 8's workflow owns the first apply.

- [ ] **Step 10: Commit**

```bash
git add infrastructure/terraform/modules/ecs infrastructure/terraform/modules/iam \
  infrastructure/terraform/modules/networking \
  infrastructure/terraform/environments/dev
git commit -m "feat: size the ECS tasks to the floor and back DATABASE_URL with Secrets Manager"
```

---
