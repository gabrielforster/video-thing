terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

resource "aws_sqs_queue" "dlq" {
  name                      = "${var.project_name}-${var.environment}-${var.queue_name}-dlq"
  message_retention_seconds = var.message_retention_seconds

  tags = {
    Name        = "${var.project_name}-${var.environment}-${var.queue_name}-dlq"
    Project     = var.project_name
    Environment = var.environment
  }
}

resource "aws_sqs_queue" "this" {
  name                       = "${var.project_name}-${var.environment}-${var.queue_name}"
  visibility_timeout_seconds = var.visibility_timeout_seconds
  message_retention_seconds  = var.message_retention_seconds

  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.dlq.arn
    maxReceiveCount     = var.max_receive_count
  })

  tags = {
    Name        = "${var.project_name}-${var.environment}-${var.queue_name}"
    Project     = var.project_name
    Environment = var.environment
  }
}

# Allows S3 to deliver bucket notifications, scoped to the raw bucket only via SourceArn to avoid
# any other bucket/account being able to enqueue messages onto this queue.
resource "aws_sqs_queue_policy" "this" {
  queue_url = aws_sqs_queue.this.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid       = "AllowS3SendMessage"
        Effect    = "Allow"
        Principal = { Service = "s3.amazonaws.com" }
        Action    = "sqs:SendMessage"
        Resource  = aws_sqs_queue.this.arn
        Condition = {
          ArnEquals = {
            "aws:SourceArn" = var.raw_bucket_arn
          }
        }
      }
    ]
  })
}
