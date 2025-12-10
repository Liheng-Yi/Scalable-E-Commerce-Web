# Region to deploy into
variable "aws_region" {
  type    = string
  default = "us-west-2"
}

# ECR & ECS settings
variable "ecr_repository_name" {
  type    = string
  default = "week6-experiment-ecr"
}

variable "service_name" {
  type    = string
  default = "week6-experiment"
}

variable "container_port" {
  type    = number
  default = 8080
}

# How long to keep logs
variable "log_retention_days" {
  type    = number
  default = 7
}

# Auto Scaling Configuration
variable "min_capacity" {
  type        = number
  default     = 1
  description = "Minimum number of ECS tasks (start with 1 for memory leak demo)"
}

variable "max_capacity" {
  type        = number
  default     = 4
  description = "Maximum number of ECS tasks"
}

variable "cpu_target_value" {
  type        = number
  default     = 70
  description = "Target CPU utilization % for auto scaling"
}

variable "memory_target_value" {
  type        = number
  default     = 70
  description = "Target memory utilization % for auto scaling"
}

variable "scale_in_cooldown" {
  type        = number
  default     = 300
  description = "Seconds to wait before scaling in"
}

variable "scale_out_cooldown" {
  type        = number
  default     = 300
  description = "Seconds to wait before scaling out"
}

# Environment name for resource tagging
variable "environment" {
  type        = string
  default     = "dev"
  description = "Environment name (dev, staging, prod)"
}
