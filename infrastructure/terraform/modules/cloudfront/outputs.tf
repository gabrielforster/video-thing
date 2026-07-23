output "distribution_id" {
  description = "ID of the CloudFront distribution."
  value       = aws_cloudfront_distribution.this.id
}

output "distribution_domain_name" {
  description = "Domain name of the CloudFront distribution (e.g. dxxxxxxxx.cloudfront.net)."
  value       = aws_cloudfront_distribution.this.domain_name
}

output "distribution_arn" {
  description = "ARN of the CloudFront distribution."
  value       = aws_cloudfront_distribution.this.arn
}

output "origin_access_control_id" {
  description = "ID of the Origin Access Control used by the distribution."
  value       = aws_cloudfront_origin_access_control.this.id
}
