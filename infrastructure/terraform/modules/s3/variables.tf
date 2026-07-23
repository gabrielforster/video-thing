variable "project_name" {
  description = "Project name used for resource naming and tagging."
  type        = string
}

variable "environment" {
  description = "Deployment environment (e.g. dev, staging, prod)."
  type        = string
}

variable "raw_bucket_name" {
  description = "Globally-unique bucket name for raw (pre-transcode) uploads."
  type        = string
}

variable "processed_bucket_name" {
  description = "Globally-unique bucket name for processed (post-transcode) assets served via CloudFront."
  type        = string
}

variable "cors_allowed_origins" {
  description = "Origins allowed to PUT/POST directly to the raw bucket for browser presigned uploads."
  type        = list(string)
  default     = ["*"]
}

variable "sqs_queue_arn" {
  description = "ARN of the SQS queue that receives raw-bucket object-created notifications."
  type        = string
}

variable "cloudfront_oac_id" {
  description = "CloudFront Origin Access Control ID for the processed bucket. May be empty until the cloudfront module runs; the bucket policy granting CloudFront read access is applied by the cloudfront module (or a follow-up apply) rather than here, to avoid a circular dependency between s3 and cloudfront."
  type        = string
  default     = ""
}
