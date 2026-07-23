output "queue_id" {
  description = "URL (ID) of the main processing queue."
  value       = aws_sqs_queue.this.id
}

output "queue_arn" {
  description = "ARN of the main processing queue."
  value       = aws_sqs_queue.this.arn
}

output "queue_url" {
  description = "URL of the main processing queue (used as worker QUEUE_URL env var)."
  value       = aws_sqs_queue.this.url
}

output "dlq_arn" {
  description = "ARN of the dead-letter queue."
  value       = aws_sqs_queue.dlq.arn
}

output "dlq_url" {
  description = "URL of the dead-letter queue."
  value       = aws_sqs_queue.dlq.url
}
