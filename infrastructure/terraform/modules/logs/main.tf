terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

resource "aws_cloudwatch_log_group" "this" {
  for_each = toset(var.log_group_names)

  name              = "/ecs/${var.project_name}-${var.environment}-${each.value}"
  retention_in_days = var.retention_in_days

  tags = {
    Name        = "/ecs/${var.project_name}-${var.environment}-${each.value}"
    Project     = var.project_name
    Environment = var.environment
  }
}
