variable "project_name" {
  description = "Name of the project, used for resource naming and tagging."
  type        = string
}

variable "environment" {
  description = "Deployment environment (e.g. dev, staging, production)."
  type        = string
}

variable "s3_raw_bucket_arn" {
  description = "ARN of the S3 bucket storing raw/uploaded video assets."
  type        = string
}

variable "s3_processed_bucket_arn" {
  description = "ARN of the S3 bucket storing processed/transcoded video assets."
  type        = string
}

variable "sqs_queue_arn" {
  description = "ARN of the SQS queue used for video processing jobs."
  type        = string
}

variable "ecr_repository_arns" {
  description = "ARNs of ECR repositories the ECS task execution role must be able to pull from."
  type        = list(string)
}
