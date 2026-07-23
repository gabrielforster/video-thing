terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

data "aws_iam_policy_document" "ecs_tasks_assume_role" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRole"]

    principals {
      type        = "Service"
      identifiers = ["ecs-tasks.amazonaws.com"]
    }
  }
}

# Used by the ECS agent to pull images and ship logs, not by application code in the container.
resource "aws_iam_role" "ecs_task_execution_role" {
  name               = "${var.project_name}-${var.environment}-ecs-task-execution-role"
  assume_role_policy = data.aws_iam_policy_document.ecs_tasks_assume_role.json

  tags = {
    Name        = "${var.project_name}-${var.environment}-ecs-task-execution-role"
    Project     = var.project_name
    Environment = var.environment
  }
}

resource "aws_iam_role_policy_attachment" "ecs_task_execution_managed" {
  role       = aws_iam_role.ecs_task_execution_role.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
}

data "aws_iam_policy_document" "ecs_task_execution_extra" {
  statement {
    sid    = "EcrPull"
    effect = "Allow"
    actions = [
      "ecr:GetAuthorizationToken",
    ]
    resources = ["*"]
  }

  statement {
    sid    = "EcrPullScoped"
    effect = "Allow"
    actions = [
      "ecr:BatchGetImage",
      "ecr:GetDownloadUrlForLayer",
    ]
    resources = var.ecr_repository_arns
  }

  statement {
    sid    = "CloudWatchLogsWrite"
    effect = "Allow"
    actions = [
      "logs:CreateLogGroup",
      "logs:CreateLogStream",
      "logs:PutLogEvents",
    ]
    resources = ["arn:aws:logs:*:*:log-group:/ecs/${var.project_name}-${var.environment}*"]
  }
}

resource "aws_iam_role_policy" "ecs_task_execution_extra" {
  name   = "${var.project_name}-${var.environment}-ecs-task-execution-extra"
  role   = aws_iam_role.ecs_task_execution_role.id
  policy = data.aws_iam_policy_document.ecs_task_execution_extra.json
}

# Assumed by application code in the API container: writes to the raw bucket (presigned
# uploads) and reads from the processed bucket.
resource "aws_iam_role" "api_task_role" {
  name               = "${var.project_name}-${var.environment}-api-task-role"
  assume_role_policy = data.aws_iam_policy_document.ecs_tasks_assume_role.json

  tags = {
    Name        = "${var.project_name}-${var.environment}-api-task-role"
    Project     = var.project_name
    Environment = var.environment
  }
}

data "aws_iam_policy_document" "api_task_policy" {
  statement {
    sid    = "RawBucketObjectWrite"
    effect = "Allow"
    actions = [
      "s3:PutObject",
    ]
    # Object-level action requires the /* suffix on the bucket ARN.
    resources = ["${var.s3_raw_bucket_arn}/*"]
  }

  statement {
    sid    = "ProcessedBucketObjectRead"
    effect = "Allow"
    actions = [
      "s3:GetObject",
    ]
    resources = ["${var.s3_processed_bucket_arn}/*"]
  }
}

resource "aws_iam_role_policy" "api_task_policy" {
  name   = "${var.project_name}-${var.environment}-api-task-policy"
  role   = aws_iam_role.api_task_role.id
  policy = data.aws_iam_policy_document.api_task_policy.json
}

# Assumed by application code in the worker container: reads raw uploads, writes processed
# output, and consumes the job queue.
resource "aws_iam_role" "worker_task_role" {
  name               = "${var.project_name}-${var.environment}-worker-task-role"
  assume_role_policy = data.aws_iam_policy_document.ecs_tasks_assume_role.json

  tags = {
    Name        = "${var.project_name}-${var.environment}-worker-task-role"
    Project     = var.project_name
    Environment = var.environment
  }
}

data "aws_iam_policy_document" "worker_task_policy" {
  statement {
    sid    = "RawBucketObjectRead"
    effect = "Allow"
    actions = [
      "s3:GetObject",
    ]
    resources = ["${var.s3_raw_bucket_arn}/*"]
  }

  statement {
    sid    = "ProcessedBucketObjectWrite"
    effect = "Allow"
    actions = [
      "s3:PutObject",
    ]
    resources = ["${var.s3_processed_bucket_arn}/*"]
  }

  statement {
    sid    = "QueueConsume"
    effect = "Allow"
    actions = [
      "sqs:ReceiveMessage",
      "sqs:DeleteMessage",
      "sqs:GetQueueAttributes",
      "sqs:ChangeMessageVisibility",
    ]
    # Queue-level (not object-level) actions use the bare queue ARN.
    resources = [var.sqs_queue_arn]
  }
}

resource "aws_iam_role_policy" "worker_task_policy" {
  name   = "${var.project_name}-${var.environment}-worker-task-policy"
  role   = aws_iam_role.worker_task_role.id
  policy = data.aws_iam_policy_document.worker_task_policy.json
}
