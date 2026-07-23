output "raw_bucket_id" {
  description = "Name (ID) of the raw uploads bucket."
  value       = aws_s3_bucket.raw.id
}

output "raw_bucket_arn" {
  description = "ARN of the raw uploads bucket."
  value       = aws_s3_bucket.raw.arn
}

output "raw_bucket_regional_domain_name" {
  description = "Regional domain name of the raw uploads bucket."
  value       = aws_s3_bucket.raw.bucket_regional_domain_name
}

output "processed_bucket_id" {
  description = "Name (ID) of the processed assets bucket."
  value       = aws_s3_bucket.processed.id
}

output "processed_bucket_arn" {
  description = "ARN of the processed assets bucket."
  value       = aws_s3_bucket.processed.arn
}

output "processed_bucket_regional_domain_name" {
  description = "Regional domain name of the processed assets bucket, used as the CloudFront origin."
  value       = aws_s3_bucket.processed.bucket_regional_domain_name
}
