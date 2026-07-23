output "cluster_id" {
  description = "ID of the ECS cluster."
  value       = aws_ecs_cluster.this.id
}

output "cluster_name" {
  description = "Name of the ECS cluster."
  value       = aws_ecs_cluster.this.name
}

output "api_service_name" {
  description = "Name of the API ECS service."
  value       = aws_ecs_service.api.name
}

output "worker_service_name" {
  description = "Name of the worker ECS service."
  value       = aws_ecs_service.worker.name
}

output "api_security_group_id" {
  description = "Security group attached to API tasks."
  value       = aws_security_group.api.id
}

output "worker_security_group_id" {
  description = "Security group attached to worker tasks."
  value       = aws_security_group.worker.id
}
