output "dashboard_name" {
  description = "Name of the CloudWatch dashboard."
  value       = aws_cloudwatch_dashboard.this.dashboard_name
}

output "alarm_arns" {
  description = "Map of alarm name to ARN, for wiring into external alerting/documentation."
  value = merge(
    { sqs_queue_depth = aws_cloudwatch_metric_alarm.sqs_queue_depth.arn },
    { alb_5xx = aws_cloudwatch_metric_alarm.alb_5xx.arn },
    { for k, a in aws_cloudwatch_metric_alarm.ecs_cpu_high : "ecs_cpu_high_${k}" => a.arn }
  )
}
