variable "project_name" {
  description = "Project name used for resource naming and tagging."
  type        = string
}

variable "environment" {
  description = "Deployment environment (e.g. dev, staging, prod)."
  type        = string
}

variable "vpc_id" {
  description = "VPC in which to create the RDS security group."
  type        = string
}

variable "private_subnet_ids" {
  description = "Private subnet IDs for the DB subnet group; the database is never placed in a public subnet."
  type        = list(string)
}

variable "db_name" {
  description = "Initial database name."
  type        = string
  default     = "videothing"
}

variable "db_username" {
  description = "Master username for the database."
  type        = string
  default     = "app"
}

variable "instance_class" {
  description = "RDS instance class."
  type        = string
  default     = "db.t4g.micro"
}

variable "allocated_storage" {
  description = "Allocated storage in GB."
  type        = number
  default     = 20
}

variable "engine_version" {
  description = "PostgreSQL engine version."
  type        = string
  default     = "16"
}

variable "allowed_security_group_ids" {
  description = "Security groups allowed to connect to Postgres (e.g. the API and worker ECS task security groups)."
  type        = list(string)
}

variable "multi_az" {
  description = "Whether to deploy a Multi-AZ standby replica."
  type        = bool
  default     = false
}

variable "backup_retention_period" {
  description = "Number of days to retain automated backups."
  type        = number
  default     = 7
}
