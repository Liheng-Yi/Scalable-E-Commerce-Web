# DynamoDB Tables for E-Commerce Application

# Products Table
resource "aws_dynamodb_table" "products" {
  name           = "${var.service_name}-products"
  billing_mode   = "PAY_PER_REQUEST"  # On-demand capacity for variable workloads
  hash_key       = "id"

  attribute {
    name = "id"
    type = "N"  # Number type for product ID
  }

  # Global Secondary Index for category searches
  global_secondary_index {
    name            = "category-index"
    hash_key        = "category"
    projection_type = "ALL"
  }

  attribute {
    name = "category"
    type = "S"
  }

  tags = {
    Name        = "${var.service_name}-products"
    Environment = var.environment
  }
}

# Carts Table
resource "aws_dynamodb_table" "carts" {
  name           = "${var.service_name}-carts"
  billing_mode   = "PAY_PER_REQUEST"
  hash_key       = "user_id"

  attribute {
    name = "user_id"
    type = "S"
  }

  # TTL for automatic cart expiration (abandoned carts)
  ttl {
    attribute_name = "ttl"
    enabled        = true
  }

  tags = {
    Name        = "${var.service_name}-carts"
    Environment = var.environment
  }
}

# Orders Table
resource "aws_dynamodb_table" "orders" {
  name           = "${var.service_name}-orders"
  billing_mode   = "PAY_PER_REQUEST"
  hash_key       = "order_id"

  attribute {
    name = "order_id"
    type = "S"
  }

  # Global Secondary Index to query orders by user
  global_secondary_index {
    name            = "user-index"
    hash_key        = "user_id"
    projection_type = "ALL"
  }

  attribute {
    name = "user_id"
    type = "S"
  }

  tags = {
    Name        = "${var.service_name}-orders"
    Environment = var.environment
  }
}

