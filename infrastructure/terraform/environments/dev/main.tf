terraform {
  required_version = ">= 1.7.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }

  # Remote state backend (S3 + DynamoDB locking) is required before this is
  # applied against a real account. Left unconfigured here since bucket/table
  # names are account-specific; wire up via `terraform init -backend-config=...`
  # or a backend.tf added per environment.
  # backend "s3" {}
}

provider "aws" {
  region = var.aws_region

  default_tags {
    tags = {
      Project     = var.project_name
      Environment = var.environment
      ManagedBy   = "terraform"
    }
  }
}

locals {
  project_name = var.project_name
  environment  = var.environment
}

module "networking" {
  source = "../../modules/networking"

  project_name         = local.project_name
  environment          = local.environment
  vpc_cidr             = var.vpc_cidr
  azs                  = var.azs
  public_subnet_cidrs  = var.public_subnet_cidrs
  private_subnet_cidrs = var.private_subnet_cidrs
  # dev is cost-conscious: one shared NAT Gateway instead of one per AZ.
  single_nat_gateway = true
}

module "ecr" {
  source = "../../modules/ecr"

  project_name = local.project_name
  environment  = local.environment
}

module "s3" {
  source = "../../modules/s3"

  project_name          = local.project_name
  environment           = local.environment
  raw_bucket_name       = "${local.project_name}-${local.environment}-raw-uploads"
  processed_bucket_name = "${local.project_name}-${local.environment}-processed-assets"
  cors_allowed_origins  = var.cors_allowed_origins
  sqs_queue_arn         = module.sqs.queue_arn
}

module "sqs" {
  source = "../../modules/sqs"

  project_name   = local.project_name
  environment    = local.environment
  queue_name     = "${local.project_name}-${local.environment}-video-processing"
  raw_bucket_arn = "arn:aws:s3:::${local.project_name}-${local.environment}-raw-uploads"

  # NOTE: raw_bucket_arn is constructed rather than passed from module.s3 to
  # break the s3 <-> sqs cycle (s3's bucket notification needs the queue ARN,
  # the queue policy needs the bucket ARN). Bucket naming is deterministic
  # (see raw_bucket_name above), so this stays in sync without a real cycle.
}

module "cloudfront" {
  source = "../../modules/cloudfront"

  project_name                          = local.project_name
  environment                           = local.environment
  processed_bucket_id                   = module.s3.processed_bucket_id
  processed_bucket_arn                  = module.s3.processed_bucket_arn
  processed_bucket_regional_domain_name = module.s3.processed_bucket_regional_domain_name
  price_class                           = var.cloudfront_price_class
}

module "iam" {
  source = "../../modules/iam"

  project_name            = local.project_name
  environment             = local.environment
  s3_raw_bucket_arn       = module.s3.raw_bucket_arn
  s3_processed_bucket_arn = module.s3.processed_bucket_arn
  sqs_queue_arn           = module.sqs.queue_arn
  ecr_repository_arns     = values(module.ecr.repository_arns)
}

module "logs" {
  source = "../../modules/logs"

  project_name      = local.project_name
  environment       = local.environment
  log_group_names   = ["api", "worker"]
  retention_in_days = var.log_retention_in_days
}

module "rds" {
  source = "../../modules/rds"

  project_name       = local.project_name
  environment        = local.environment
  vpc_id             = module.networking.vpc_id
  private_subnet_ids = module.networking.private_subnet_ids

  # API and worker task security groups both need DB access; these are
  # created by the ecs module, so this list is filled in after ecs exists.
  allowed_security_group_ids = [
    module.ecs.api_security_group_id,
    module.ecs.worker_security_group_id,
  ]

  instance_class = var.rds_instance_class
  multi_az       = false
}

module "alb" {
  source = "../../modules/alb"

  project_name       = local.project_name
  environment        = local.environment
  vpc_id             = module.networking.vpc_id
  public_subnet_ids  = module.networking.public_subnet_ids
  health_check_path  = "/healthz"
  api_container_port = var.api_container_port
}

module "ecs" {
  source = "../../modules/ecs"

  project_name       = local.project_name
  environment        = local.environment
  vpc_id             = module.networking.vpc_id
  private_subnet_ids = module.networking.private_subnet_ids
  aws_region         = var.aws_region

  ecs_task_execution_role_arn = module.iam.ecs_task_execution_role_arn
  api_task_role_arn           = module.iam.api_task_role_arn
  worker_task_role_arn        = module.iam.worker_task_role_arn

  api_image    = "${module.ecr.repository_urls["api"]}:${var.api_image_tag}"
  worker_image = "${module.ecr.repository_urls["worker"]}:${var.worker_image_tag}"

  api_container_port    = var.api_container_port
  api_desired_count     = var.api_desired_count
  alb_security_group_id = module.alb.alb_security_group_id
  alb_target_group_arn  = module.alb.api_target_group_arn

  worker_min_count = var.worker_min_count
  worker_max_count = var.worker_max_count
  sqs_queue_arn    = module.sqs.queue_arn
  sqs_queue_url    = module.sqs.queue_url

  api_log_group_name    = module.logs.log_group_names["api"]
  worker_log_group_name = module.logs.log_group_names["worker"]

  api_env_vars = {
    DATABASE_SECRET_ARN = module.rds.db_secret_arn
    RAW_BUCKET          = module.s3.raw_bucket_id
    PROCESSED_BUCKET    = module.s3.processed_bucket_id
    CDN_DOMAIN          = module.cloudfront.distribution_domain_name
  }

  worker_env_vars = {
    DATABASE_SECRET_ARN = module.rds.db_secret_arn
    RAW_BUCKET          = module.s3.raw_bucket_id
    PROCESSED_BUCKET    = module.s3.processed_bucket_id
  }
}

module "monitoring" {
  source = "../../modules/monitoring"

  project_name                = local.project_name
  environment                 = local.environment
  ecs_cluster_name            = module.ecs.cluster_name
  api_service_name            = module.ecs.api_service_name
  worker_service_name         = module.ecs.worker_service_name
  sqs_queue_name              = "${local.project_name}-${local.environment}-video-processing"
  alb_arn_suffix              = module.alb.alb_arn_suffix
  api_target_group_arn_suffix = module.alb.api_target_group_arn_suffix
  sns_alarm_topic_arn         = var.sns_alarm_topic_arn
}
