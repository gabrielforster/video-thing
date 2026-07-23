output "db_endpoint" {
  description = "Connection endpoint (host:port) of the database instance."
  value       = aws_db_instance.this.endpoint
}

output "db_port" {
  description = "Port the database is listening on."
  value       = aws_db_instance.this.port
}

output "db_security_group_id" {
  description = "Security group attached to the RDS instance."
  value       = aws_security_group.rds.id
}

output "db_secret_arn" {
  description = "ARN of the Secrets Manager secret holding DB credentials, for ECS task secrets injection."
  value       = aws_secretsmanager_secret.db.arn
}
