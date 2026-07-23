output "log_group_arns" {
  description = "Map of log group name (e.g. api, worker) to its ARN."
  value       = { for name, lg in aws_cloudwatch_log_group.this : name => lg.arn }
}

output "log_group_names" {
  description = "Map of log group name (e.g. api, worker) to its full CloudWatch log group name string."
  value       = { for name, lg in aws_cloudwatch_log_group.this : name => lg.name }
}
