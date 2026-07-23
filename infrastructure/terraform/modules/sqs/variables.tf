variable "project_name" {
  description = "Project name used for resource naming and tagging."
  type        = string
}

variable "environment" {
  description = "Deployment environment (e.g. dev, staging, prod)."
  type        = string
}

variable "queue_name" {
  description = "Logical name of the processing queue."
  type        = string
  default     = "video-processing"
}

variable "visibility_timeout_seconds" {
  description = "Visibility timeout for the main queue; long enough to cover a full transcode job so it isn't redelivered mid-flight."
  type        = number
  default     = 900
}

variable "message_retention_seconds" {
  description = "How long messages are retained in the queue (default 14 days, the SQS max)."
  type        = number
  default     = 1209600
}

variable "max_receive_count" {
  description = "Number of delivery attempts before a message is moved to the DLQ."
  type        = number
  default     = 5
}

variable "raw_bucket_arn" {
  description = "ARN of the raw uploads S3 bucket, used to scope the queue policy to only that bucket's notifications."
  type        = string
}
