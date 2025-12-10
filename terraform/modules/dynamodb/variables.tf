variable "service_name" {
  description = "Name prefix for DynamoDB tables"
  type        = string
}

variable "environment" {
  description = "Environment name (e.g., dev, prod)"
  type        = string
  default     = "dev"
}

