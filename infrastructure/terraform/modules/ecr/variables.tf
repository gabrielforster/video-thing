variable "project_name" {
  description = "Name of the project, used for resource naming and tagging."
  type        = string
}

variable "environment" {
  description = "Deployment environment (e.g. dev, staging, production)."
  type        = string
}

variable "repository_names" {
  description = "Names of the ECR repositories to create (one per container image)."
  type        = list(string)
  default     = ["api", "worker", "web"]
}

variable "image_tag_mutability" {
  description = "Tag mutability setting for the repositories (MUTABLE or IMMUTABLE)."
  type        = string
  default     = "IMMUTABLE"
}

variable "scan_on_push" {
  description = "Whether to scan images for vulnerabilities on push."
  type        = bool
  default     = true
}
