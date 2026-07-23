variable "project_name" {
  description = "Name of the project, used for resource naming and tagging."
  type        = string
}

variable "environment" {
  description = "Deployment environment (e.g. dev, staging, production)."
  type        = string
}

variable "vpc_cidr" {
  description = "CIDR block for the VPC."
  type        = string
  default     = "10.0.0.0/16"
}

variable "azs" {
  description = "List of availability zones to spread subnets across."
  type        = list(string)
}

variable "public_subnet_cidrs" {
  description = "CIDR blocks for public subnets, one per entry (mapped to azs by index)."
  type        = list(string)
}

variable "private_subnet_cidrs" {
  description = "CIDR blocks for private subnets, one per entry (mapped to azs by index)."
  type        = list(string)
}

variable "single_nat_gateway" {
  description = "If true, provision a single shared NAT gateway (cost-conscious for dev/staging). If false, provision one NAT gateway per AZ for production-grade HA."
  type        = bool
  default     = true
}
