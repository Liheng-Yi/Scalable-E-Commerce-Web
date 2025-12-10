variable "service_name" {
  description = "Name prefix for Redis resources"
  type        = string
}

variable "vpc_id" {
  description = "VPC ID where Redis will be deployed"
  type        = string
}

variable "subnet_ids" {
  description = "Subnet IDs for Redis subnet group"
  type        = list(string)
}

variable "ecs_security_group_ids" {
  description = "Security group IDs of ECS tasks (allowed to connect to Redis)"
  type        = list(string)
}

variable "node_type" {
  description = "ElastiCache node type"
  type        = string
  default     = "cache.t3.micro"  # Free tier eligible
}

variable "environment" {
  description = "Environment name"
  type        = string
  default     = "dev"
}

