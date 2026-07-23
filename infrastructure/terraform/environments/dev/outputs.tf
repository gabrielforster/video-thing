output "alb_dns_name" {
  description = "Public DNS name of the API load balancer."
  value       = module.alb.alb_dns_name
}

output "cdn_domain_name" {
  description = "CloudFront domain name serving processed HLS assets."
  value       = module.cloudfront.distribution_domain_name
}

output "db_endpoint" {
  description = "RDS PostgreSQL endpoint."
  value       = module.rds.db_endpoint
  sensitive   = true
}

output "raw_bucket_id" {
  description = "Name of the raw uploads bucket."
  value       = module.s3.raw_bucket_id
}

output "processed_bucket_id" {
  description = "Name of the processed assets bucket."
  value       = module.s3.processed_bucket_id
}

output "ecs_cluster_name" {
  description = "Name of the ECS cluster."
  value       = module.ecs.cluster_name
}

output "dashboard_name" {
  description = "Name of the CloudWatch dashboard."
  value       = module.monitoring.dashboard_name
}
