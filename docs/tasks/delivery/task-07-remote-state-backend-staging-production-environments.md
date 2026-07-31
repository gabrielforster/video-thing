# Task 7: Remote state backend and the staging/production environments

> Task 7 of 9 in [`delivery`](00-context.md). Read [`00-context.md`](00-context.md) first — the goal, tech stack, Global Constraints, and file structure bind this task. Full plan: [`delivery-plan.md`](../../plans/delivery-plan.md).
>
> Previous: [Task 6](task-06-one-time-aws-bootstrap-state-bucket.md) · Next: [Task 8](task-08-ci-deploy-workflows.md)

---

**Files:**
- Modify: `infrastructure/terraform/environments/dev/main.tf` (`required_version`, parameterise the deltas, drop the commented backend)
- Modify: `infrastructure/terraform/environments/dev/variables.tf` (append six variables, lower four to the floor)
- Create: `infrastructure/terraform/environments/dev/backend.tf`
- Create: `infrastructure/terraform/environments/staging/{main.tf,variables.tf,outputs.tf,backend.tf}`
- Create: `infrastructure/terraform/environments/production/{main.tf,variables.tf,outputs.tf,backend.tf}`

**Design:** after this task `main.tf` and `outputs.tf` are byte-identical in all three directories; every environment difference lives in `variables.tf` defaults and `backend.tf`'s state key. That makes a cross-environment diff a one-line `diff` and makes "does staging match dev" a mechanical question instead of a reading exercise.

**Interfaces:**
- Consumes: the state bucket created in Task 6; the `rds` module's `skip_final_snapshot`/`deletion_protection` from Task 2; the `migrations` module from Task 2.
- Produces: three environment directories that each pass `terraform fmt -check` and `terraform init -backend=false && terraform validate`.

- [ ] **Step 1: Parameterise the last hardcoded environment deltas in `dev/main.tf`**

In `module "networking"`, replace:

```hcl
  # dev is cost-conscious: one shared NAT Gateway instead of one per AZ.
  single_nat_gateway = true
```

with:

```hcl
  # One shared NAT Gateway in every environment, production included. It bills $32.85/month
  # idle; the five interface endpoints that would replace it cost $36.50 in one AZ and $73.00
  # in two. The cost is that losing its AZ costs egress for private subnets in both. See
  # "What costs money while idle" for the full arithmetic.
  single_nat_gateway = var.single_nat_gateway
```

In `module "rds"`, replace:

```hcl
  instance_class = var.rds_instance_class
  multi_az       = false
```

with:

```hcl
  instance_class          = var.rds_instance_class
  allocated_storage       = var.rds_allocated_storage
  backup_retention_period = var.rds_backup_retention_period
  multi_az                = var.rds_multi_az
  skip_final_snapshot     = var.rds_skip_final_snapshot
  deletion_protection     = var.rds_deletion_protection
```

`module "ecs"` already received `api_cpu`, `api_memory`, `worker_cpu`, and `worker_memory` in Task 3 step 5, and those four variables are already declared in `dev/variables.tf`. Nothing to add here; step 5 and step 6 carry them into the other two environments.

Finally, in the `terraform` block at the top of the file, raise the version floor and delete the commented-out backend, replacing lines 1-16 with:

```hcl
terraform {
  # 1.10 is the floor because backend.tf uses S3 native state locking (use_lockfile),
  # which replaces the DynamoDB lock table ADR-0005 originally called for.
  required_version = ">= 1.10.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}
```

- [ ] **Step 2: Append the six remaining variables to `dev/variables.tf` and take the rest to the floor**

First lower the three size defaults already in the file to the floor, so `dev` stops asking for more than a demo needs:

| Variable | Was | Now | Why |
|---|---|---|---|
| `log_retention_in_days` | `14` | `7` | Retention is not the cost driver — CloudWatch bills $0.50/GB to *ingest* and $0.03/GB-month to retain, so a month of logs at ~1 GB is $0.007/month to keep for 7 days versus $0.001 for 1. Going below a week saves a fraction of a cent and costs the ability to read yesterday's failure, so 7 is the floor worth taking. |
| `rds_instance_class` | `db.t4g.micro` | `db.t4g.micro` | Already the smallest Postgres class. Unchanged. |
| `api_desired_count` | `1` | `1` | Already the floor. Unchanged — and with `deployment_minimum_healthy_percent = 100` a deploy still starts the new task before draining the old. |
| `worker_max_count` | `4` | `2` | Two concurrent transcodes is enough to show the queue-depth scaling in §14 actually works; the ceiling only bounds a burst, and nothing bills until a message arrives. |

Then append:

```hcl
variable "single_nat_gateway" {
  description = "One shared NAT Gateway instead of one per AZ. True everywhere, production included: one NAT is $32.85/month against $36.50 for the five interface endpoints that would replace it in a single AZ. The cost is that losing its AZ costs egress for private subnets in both."
  type        = bool
  default     = true
}

variable "rds_allocated_storage" {
  description = "RDS allocated storage in GB. 20 is the minimum for a gp3 Postgres instance."
  type        = number
  default     = 20
}

variable "rds_backup_retention_period" {
  description = "Days of automated RDS backups to retain. Not lowered to 1: backup storage up to 100% of allocated storage is free, so a week of backups on a 20 GB instance costs nothing and dropping to a day would save nothing."
  type        = number
  default     = 7
}

variable "rds_multi_az" {
  description = "Deploy a Multi-AZ standby replica. Doubles the instance and storage cost; true only in production, where spec 6 requires it."
  type        = bool
  default     = false
}

variable "rds_skip_final_snapshot" {
  description = "Skip the final snapshot on destroy. False in production, per spec 6."
  type        = bool
  default     = true
}

variable "rds_deletion_protection" {
  description = "Block deletion of the RDS instance. True in production."
  type        = bool
  default     = false
}
```

`api_cpu`, `api_memory`, `worker_cpu`, and `worker_memory` are already declared here from Task 3 step 5 — do not add them again, or `terraform validate` fails with `Duplicate variable declaration`.

- [ ] **Step 3: Write the three `backend.tf` files**

`infrastructure/terraform/environments/dev/backend.tf`:

```hcl
# `use_lockfile` is S3 native locking (Terraform 1.10+): the lock is a .tflock object next
# to the state file, so no DynamoDB table is needed. This deviates from ADR-0005 and from
# docs/specifications/github-oidc-role.md 3.1, whose DynamoDB grant is now unnecessary.
#
# The bucket must already exist -- scripts/bootstrap-aws.sh creates it. `terraform validate`
# does not need it: use `terraform init -backend=false` for validation-only runs.
terraform {
  backend "s3" {
    bucket       = "video-thing-terraform-state"
    key          = "environments/dev/terraform.tfstate"
    region       = "us-east-1"
    encrypt      = true
    use_lockfile = true
  }
}
```

`staging/backend.tf` and `production/backend.tf` are the same file with `key` set to `environments/staging/terraform.tfstate` and `environments/production/terraform.tfstate`. Write all three out in full — a shared key would let two environments overwrite each other's state, which is the one mistake in this file that is unrecoverable.

- [ ] **Step 4: Copy `main.tf` and `outputs.tf` into the two new directories**

```bash
cd infrastructure/terraform/environments
mkdir -p staging production
cp dev/main.tf dev/outputs.tf staging/
cp dev/main.tf dev/outputs.tf production/
diff dev/main.tf staging/main.tf && diff dev/main.tf production/main.tf && echo "identical"
cd ../../..
```

Expected: `identical`. Nothing in `main.tf` names an environment — `local.environment` comes from `var.environment`, whose default is the only per-directory difference.

- [ ] **Step 5: Write `staging/variables.tf`**

```hcl
variable "project_name" {
  description = "Project name used for resource naming and tagging."
  type        = string
  default     = "video-thing"
}

variable "environment" {
  description = "Deployment environment."
  type        = string
  default     = "staging"
}

variable "aws_region" {
  description = "AWS region to deploy into."
  type        = string
  default     = "us-east-1"
}

variable "vpc_cidr" {
  description = "CIDR block for the VPC. Distinct from dev's so the two can be peered later without renumbering."
  type        = string
  default     = "10.1.0.0/16"
}

variable "azs" {
  description = "Availability zones to spread subnets across."
  type        = list(string)
  default     = ["us-east-1a", "us-east-1b"]
}

variable "public_subnet_cidrs" {
  description = "CIDR blocks for public subnets (one per AZ)."
  type        = list(string)
  default     = ["10.1.0.0/24", "10.1.1.0/24"]
}

variable "private_subnet_cidrs" {
  description = "CIDR blocks for private subnets (one per AZ)."
  type        = list(string)
  default     = ["10.1.10.0/24", "10.1.11.0/24"]
}

variable "single_nat_gateway" {
  description = "One shared NAT Gateway instead of one per AZ. See dev/variables.tf for the arithmetic."
  type        = bool
  default     = true
}

variable "cors_allowed_origins" {
  description = "Origins allowed to upload to the raw bucket and fetch HLS from the CDN."
  type        = list(string)
  default     = ["*"]
}

variable "cloudfront_price_class" {
  description = "CloudFront price class."
  type        = string
  default     = "PriceClass_100"
}

variable "log_retention_in_days" {
  description = "CloudWatch Logs retention for API/worker/migration log groups. Ingestion, not retention, is the cost driver."
  type        = number
  default     = 7
}

variable "rds_instance_class" {
  description = "RDS instance class."
  type        = string
  default     = "db.t4g.micro"
}

variable "rds_allocated_storage" {
  description = "RDS allocated storage in GB."
  type        = number
  default     = 20
}

variable "rds_backup_retention_period" {
  description = "Days of automated RDS backups to retain."
  type        = number
  default     = 7
}

variable "rds_multi_az" {
  description = "Deploy a Multi-AZ standby replica."
  type        = bool
  default     = false
}

variable "rds_skip_final_snapshot" {
  description = "Skip the final snapshot on destroy."
  type        = bool
  default     = true
}

variable "rds_deletion_protection" {
  description = "Block deletion of the RDS instance."
  type        = bool
  default     = false
}

variable "api_container_port" {
  description = "Port the API container listens on."
  type        = number
  default     = 8080
}

variable "api_desired_count" {
  description = "Desired API task count. One is the floor; deployment_minimum_healthy_percent = 100 still starts the replacement before draining the old task."
  type        = number
  default     = 1
}

variable "api_cpu" {
  description = "Fargate CPU units for the API task. 256 is the Fargate floor."
  type        = number
  default     = 256
}

variable "api_memory" {
  description = "Fargate memory (MiB) for the API task. 512 is the smallest value valid with 256 CPU units."
  type        = number
  default     = 512
}

variable "worker_cpu" {
  description = "Fargate CPU units for the worker task. 1024 (1 vCPU), not the 256 floor: the ladder runs four sequential x264 encodes and at 0.5 vCPU a modest clip outruns the 900s visibility timeout. See Task 3."
  type        = number
  default     = 1024
}

variable "worker_memory" {
  description = "Fargate memory (MiB) for the worker task. 2048 is the smallest value valid with 1024 CPU units."
  type        = number
  default     = 2048
}

variable "worker_min_count" {
  description = "Minimum worker task count (0 = scale to zero when idle)."
  type        = number
  default     = 0
}

variable "worker_max_count" {
  description = "Maximum worker task count; only bounds a burst, nothing bills until a message arrives."
  type        = number
  default     = 2
}

variable "api_image_tag" {
  description = "Image tag to deploy for the API service."
  type        = string
  default     = "latest"
}

variable "worker_image_tag" {
  description = "Image tag to deploy for the worker service."
  type        = string
  default     = "latest"
}

variable "migrations_image_tag" {
  description = "Image tag to deploy for the one-off migrations task."
  type        = string
  default     = "latest"
}

variable "sns_alarm_topic_arn" {
  description = "SNS topic ARN for alarm notifications. Empty means alarms are created without actions."
  type        = string
  default     = ""
}
```

- [ ] **Step 6: Write `production/variables.tf`**

Identical to staging's file except for the eight values below. Write the whole file, copying step 5's text and replacing these defaults. Note how short the list is: production is not a bigger deployment, it is the same deployment with different *safety* settings, because §6 of the master spec names `multi_az` and `skip_final_snapshot` as the production deltas and neither is a size.

| Variable | production default | Why |
|---|---|---|
| `environment` | `"production"` | |
| `vpc_cidr` | `"10.2.0.0/16"` | non-overlapping with dev (`10.0`) and staging (`10.1`) |
| `public_subnet_cidrs` | `["10.2.0.0/24", "10.2.1.0/24"]` | one per AZ; `azs` stays at two, which is the minimum an ALB and a Multi-AZ subnet group require |
| `private_subnet_cidrs` | `["10.2.10.0/24", "10.2.11.0/24"]` | one per AZ |
| `rds_multi_az` | `true` | §6 names this. **The one deliberately non-minimal choice in the whole plan** — it doubles the instance and storage line to $27.96/month. It is what makes production production. |
| `rds_skip_final_snapshot` | `false` | §6; a destroy must leave a recoverable snapshot |
| `rds_deletion_protection` | `true` | `terraform destroy` fails until a human turns this off, deliberately |
| `log_retention_in_days` | `30` | the one retention bump: a production incident is often reconstructed days later, and at ~1 GB/month this is about two cents |

Everything else stays at staging's value: `db.t4g.micro`, 20 GB, `api_desired_count = 1`, `api_cpu = 256`, `api_memory = 512`, `worker_cpu = 1024`, `worker_memory = 2048`, `worker_max_count = 2`, `single_nat_gateway = true`, `cloudfront_price_class = "PriceClass_100"`, `azs = ["us-east-1a", "us-east-1b"]`.

Also update the `vpc_cidr` and `environment` descriptions to say `production`.

**Be honest about what this production is.** A Multi-AZ database behind a single NAT Gateway and a single API task is partial availability, not high availability: the database survives losing an AZ, and egress and the API do not. That is the correct shape for a demo whose `production` exists to prove the deployment path works, and it is the wrong shape for anything with users. Raising `api_desired_count` to 2 and `single_nat_gateway` to `false` is the two-line change that fixes it, at about +$42/month.

- [ ] **Step 7: Validate all three environments**

```bash
cd infrastructure/terraform
terraform fmt -check -recursive
for env in dev staging production; do
  echo "== $env"
  (cd "environments/$env" && terraform init -backend=false >/dev/null && terraform validate)
done
cd ../..
```

Expected: `fmt -check` prints nothing, and each environment prints `Success! The configuration is valid.` If an environment fails with a subnet/AZ length error, its two CIDR lists and its `azs` list are out of step — `modules/networking/main.tf` maps subnets to AZs by index, so all three lists must be the same length.

Then confirm the only differences between the three directories are the ones this task intends:

```bash
cd infrastructure/terraform/environments
diff dev/main.tf staging/main.tf && diff dev/main.tf production/main.tf && echo "main.tf identical"
diff dev/outputs.tf production/outputs.tf && echo "outputs.tf identical"
diff <(grep -o 'default *= *.*' dev/variables.tf) <(grep -o 'default *= *.*' production/variables.tf) | head -30
cd ../../..
```

Expected: both `identical` lines, and the last `diff` shows only the eight production values from step 6 plus `environment` and the CIDRs. Anything else in that diff is drift between environments that nobody decided on.

- [ ] **Step 8: Confirm the three state keys are distinct**

```bash
grep -h 'key ' infrastructure/terraform/environments/*/backend.tf
```

Expected: exactly three lines, `environments/dev/...`, `environments/staging/...`, `environments/production/...`. Two identical keys would make two environments share one state file.

- [ ] **Step 9: Commit**

```bash
git add infrastructure/terraform/environments
git commit -m "feat: add remote state backends and the staging and production environments"
```

---
