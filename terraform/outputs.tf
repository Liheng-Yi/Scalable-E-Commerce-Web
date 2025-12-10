output "alb_dns_name" {
  description = "DNS name of the Application Load Balancer"
  value       = module.ecs.alb_dns_name
}

output "ecr_repository_url" {
  description = "ECR repository URL"
  value       = module.ecr.repository_url
}

output "ecs_cluster_name" {
  description = "Name of the ECS cluster"
  value       = module.ecs.cluster_name
}

output "ecs_service_name" {
  description = "Name of the ECS service"
  value       = module.ecs.service_name
}

output "cloudwatch_log_group" {
  description = "CloudWatch log group name"
  value       = module.logging.log_group_name
}

output "aws_region" {
  description = "AWS region"
  value       = var.aws_region
}

# DynamoDB Table Outputs
output "dynamodb_products_table" {
  description = "DynamoDB products table name"
  value       = module.dynamodb.products_table_name
}

output "dynamodb_carts_table" {
  description = "DynamoDB carts table name"
  value       = module.dynamodb.carts_table_name
}

output "dynamodb_orders_table" {
  description = "DynamoDB orders table name"
  value       = module.dynamodb.orders_table_name
}