variable "project_name" {
  description = "Name of the project, used for resource naming and tagging."
  type        = string
}

variable "environment" {
  description = "Deployment environment (e.g. dev, staging, production)."
  type        = string
}

variable "vpc_id" {
  description = "ID of the VPC the ALB and target group live in."
  type        = string
}

variable "public_subnet_ids" {
  description = "IDs of the public subnets to place the internet-facing ALB in."
  type        = list(string)
}

variable "health_check_path" {
  description = "Path the ALB target group uses for health checks against the API service."
  type        = string
  default     = "/healthz"
}

variable "api_container_port" {
  description = "Port the API container listens on (target group port for Fargate IP-mode targets)."
  type        = number
  default     = 8080
}
