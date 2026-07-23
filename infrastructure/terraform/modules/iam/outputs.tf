output "ecs_task_execution_role_arn" {
  description = "ARN of the shared ECS task execution role (used by the ECS agent for image pull / log shipping)."
  value       = aws_iam_role.ecs_task_execution_role.arn
}

output "api_task_role_arn" {
  description = "ARN of the API service's task role (used by application code in the API container)."
  value       = aws_iam_role.api_task_role.arn
}

output "worker_task_role_arn" {
  description = "ARN of the worker service's task role (used by application code in the worker container)."
  value       = aws_iam_role.worker_task_role.arn
}
