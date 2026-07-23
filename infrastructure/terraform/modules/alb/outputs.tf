output "alb_arn" {
  description = "ARN of the Application Load Balancer."
  value       = aws_lb.this.arn
}

output "alb_dns_name" {
  description = "DNS name of the Application Load Balancer."
  value       = aws_lb.this.dns_name
}

output "alb_security_group_id" {
  description = "ID of the ALB's security group."
  value       = aws_security_group.alb.id
}

output "alb_arn_suffix" {
  description = "ARN suffix of the ALB, used for CloudWatch metric dimensions."
  value       = aws_lb.this.arn_suffix
}

output "api_target_group_arn" {
  description = "ARN of the API target group, used by the ECS service's load_balancer block."
  value       = aws_lb_target_group.api.arn
}

output "api_target_group_arn_suffix" {
  description = "ARN suffix of the API target group, used for CloudWatch metric dimensions."
  value       = aws_lb_target_group.api.arn_suffix
}
