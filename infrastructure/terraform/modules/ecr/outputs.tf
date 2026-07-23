output "repository_urls" {
  description = "Map of repository name to repository URL (for docker push/pull and task definitions)."
  value       = { for name, repo in aws_ecr_repository.this : name => repo.repository_url }
}

output "repository_arns" {
  description = "Map of repository name to repository ARN (for IAM policy scoping)."
  value       = { for name, repo in aws_ecr_repository.this : name => repo.arn }
}
