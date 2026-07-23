variable "project_name" {
  description = "Project name used for resource naming and tagging."
  type        = string
  default     = "video-platform"
}

variable "environment" {
  description = "Deployment environment."
  type        = string
  default     = "dev"
}

variable "aws_region" {
  description = "AWS region to deploy into."
  type        = string
  default     = "us-east-1"
}

variable "vpc_cidr" {
  description = "CIDR block for the VPC."
  type        = string
  default     = "10.0.0.0/16"
}

variable "azs" {
  description = "Availability zones to spread subnets across."
  type        = list(string)
  default     = ["us-east-1a", "us-east-1b"]
}

variable "public_subnet_cidrs" {
  description = "CIDR blocks for public subnets (one per AZ)."
  type        = list(string)
  default     = ["10.0.0.0/24", "10.0.1.0/24"]
}

variable "private_subnet_cidrs" {
  description = "CIDR blocks for private subnets (one per AZ)."
  type        = list(string)
  default     = ["10.0.10.0/24", "10.0.11.0/24"]
}

variable "cors_allowed_origins" {
  description = "Origins allowed to upload directly to the raw bucket."
  type        = list(string)
  default     = ["*"]
}

variable "cloudfront_price_class" {
  description = "CloudFront price class."
  type        = string
  default     = "PriceClass_100"
}

variable "log_retention_in_days" {
  description = "CloudWatch Logs retention for API/worker log groups."
  type        = number
  default     = 14
}

variable "rds_instance_class" {
  description = "RDS instance class for dev."
  type        = string
  default     = "db.t4g.micro"
}

variable "api_container_port" {
  description = "Port the API container listens on."
  type        = number
  default     = 8080
}

variable "api_desired_count" {
  description = "Desired API task count in dev."
  type        = number
  default     = 1
}

variable "worker_min_count" {
  description = "Minimum worker task count in dev (0 = scale to zero when idle)."
  type        = number
  default     = 0
}

variable "worker_max_count" {
  description = "Maximum worker task count in dev."
  type        = number
  default     = 4
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

variable "sns_alarm_topic_arn" {
  description = "SNS topic ARN for alarm notifications. Empty in dev (no on-call paging)."
  type        = string
  default     = ""
}
