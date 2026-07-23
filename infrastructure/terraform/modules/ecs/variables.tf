variable "project_name" {
  description = "Project name used for resource naming and tagging."
  type        = string
}

variable "environment" {
  description = "Deployment environment (e.g. dev, staging, prod)."
  type        = string
}

variable "vpc_id" {
  description = "VPC in which to create the API/worker security groups."
  type        = string
}

variable "private_subnet_ids" {
  description = "Private subnet IDs for API and worker Fargate tasks."
  type        = list(string)
}

variable "ecs_task_execution_role_arn" {
  description = "IAM role ECS uses to pull images and write logs (from the iam module)."
  type        = string
}

variable "api_task_role_arn" {
  description = "IAM role assumed by the API container's application code (from the iam module)."
  type        = string
}

variable "worker_task_role_arn" {
  description = "IAM role assumed by the worker container's application code (from the iam module)."
  type        = string
}

variable "api_image" {
  description = "Fully-qualified image URI for the API container, e.g. ecr repository_urls[\"api\"] + \":tag\"."
  type        = string
}

variable "worker_image" {
  description = "Fully-qualified image URI for the worker container."
  type        = string
}

variable "api_cpu" {
  description = "Fargate CPU units for the API task."
  type        = number
  default     = 512
}

variable "api_memory" {
  description = "Fargate memory (MiB) for the API task."
  type        = number
  default     = 1024
}

variable "api_desired_count" {
  description = "Desired count of API tasks; kept >= 2 for basic HA behind the ALB."
  type        = number
  default     = 2
}

variable "api_container_port" {
  description = "Port the API container listens on."
  type        = number
  default     = 8080
}

variable "alb_security_group_id" {
  description = "Security group of the ALB, used to scope API task ingress to ALB traffic only."
  type        = string
}

variable "alb_target_group_arn" {
  description = "ALB target group ARN the API service registers tasks into."
  type        = string
}

variable "worker_cpu" {
  description = "Fargate CPU units for the worker task (higher than API since transcoding is CPU-bound)."
  type        = number
  default     = 1024
}

variable "worker_memory" {
  description = "Fargate memory (MiB) for the worker task."
  type        = number
  default     = 2048
}

variable "worker_min_count" {
  description = "Minimum worker task count; 0 allows scale-to-zero when the queue is empty."
  type        = number
  default     = 0
}

variable "worker_max_count" {
  description = "Maximum worker task count for autoscaling."
  type        = number
  default     = 10
}

variable "sqs_queue_arn" {
  description = "ARN of the processing queue; used to grant/derive the queue name for the scaling metric."
  type        = string
}

variable "sqs_queue_url" {
  description = "URL of the processing queue, injected into the worker as QUEUE_URL."
  type        = string
}

variable "api_log_group_name" {
  description = "CloudWatch log group name for the API container."
  type        = string
}

variable "worker_log_group_name" {
  description = "CloudWatch log group name for the worker container."
  type        = string
}

variable "aws_region" {
  description = "AWS region, used in the awslogs log configuration."
  type        = string
  default     = "us-east-1"
}

variable "api_env_vars" {
  description = "Additional environment variables for the API container."
  type        = map(string)
  default     = {}
}

variable "worker_env_vars" {
  description = "Additional environment variables for the worker container (QUEUE_URL is added automatically)."
  type        = map(string)
  default     = {}
}
