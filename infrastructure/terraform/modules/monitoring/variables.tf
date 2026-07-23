variable "project_name" {
  description = "Project name used for resource naming and tagging."
  type        = string
}

variable "environment" {
  description = "Deployment environment (e.g. dev, staging, prod)."
  type        = string
}

variable "ecs_cluster_name" {
  description = "Name of the ECS cluster hosting the API and worker services."
  type        = string
}

variable "api_service_name" {
  description = "Name of the API ECS service."
  type        = string
}

variable "worker_service_name" {
  description = "Name of the worker ECS service."
  type        = string
}

variable "sqs_queue_name" {
  description = "Name of the SQS processing queue (not the ARN/URL) for CloudWatch metric dimensions."
  type        = string
}

variable "alb_arn_suffix" {
  description = "ALB ARN suffix (e.g. app/my-alb/abc123), used for ALB CloudWatch metric dimensions."
  type        = string
}

variable "api_target_group_arn_suffix" {
  description = "Target group ARN suffix, used for TargetResponseTime metric dimensions."
  type        = string
}

variable "sns_alarm_topic_arn" {
  description = "SNS topic to notify on alarm state changes. If empty, alarms are created without alarm_actions."
  type        = string
  default     = ""
}
