variable "project_name" {
  description = "Project name used for resource naming and tagging."
  type        = string
}

variable "environment" {
  description = "Deployment environment (e.g. dev, staging, prod)."
  type        = string
}

variable "processed_bucket_id" {
  description = "Name (ID) of the processed assets S3 bucket, from the s3 module."
  type        = string
}

variable "processed_bucket_arn" {
  description = "ARN of the processed assets S3 bucket, from the s3 module."
  type        = string
}

variable "processed_bucket_regional_domain_name" {
  description = "Regional domain name of the processed assets bucket, used as the CloudFront origin."
  type        = string
}

variable "price_class" {
  description = "CloudFront price class controlling which edge locations are used."
  type        = string
  default     = "PriceClass_100"
}
