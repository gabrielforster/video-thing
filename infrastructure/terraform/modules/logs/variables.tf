variable "project_name" {
  description = "Project name used for resource naming and tagging."
  type        = string
}

variable "environment" {
  description = "Deployment environment (e.g. dev, staging, prod)."
  type        = string
}

variable "log_group_names" {
  description = "Logical names of the ECS services that need a log group (e.g. api, worker)."
  type        = list(string)
  default     = ["api", "worker"]
}

variable "retention_in_days" {
  description = "CloudWatch Logs retention period applied to every log group."
  type        = number
  default     = 30
}
