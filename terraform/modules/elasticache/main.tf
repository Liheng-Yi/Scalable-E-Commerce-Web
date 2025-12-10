# ElastiCache Redis for Hot Product Caching

# Redis Subnet Group (uses same subnets as ECS)
resource "aws_elasticache_subnet_group" "this" {
  name       = "${var.service_name}-redis-subnet"
  subnet_ids = var.subnet_ids

  tags = {
    Name = "${var.service_name}-redis-subnet"
  }
}

# Security Group for Redis
resource "aws_security_group" "redis" {
  name        = "${var.service_name}-redis-sg"
  description = "Security group for ElastiCache Redis"
  vpc_id      = var.vpc_id

  # Allow inbound Redis traffic from ECS tasks
  ingress {
    description     = "Redis from ECS"
    from_port       = 6379
    to_port         = 6379
    protocol        = "tcp"
    security_groups = var.ecs_security_group_ids
  }

  # Allow all outbound traffic
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = {
    Name = "${var.service_name}-redis-sg"
  }
}

# ElastiCache Redis Cluster (single node for dev/testing)
resource "aws_elasticache_cluster" "this" {
  cluster_id           = "${var.service_name}-redis"
  engine               = "redis"
  engine_version       = "7.0"
  node_type            = var.node_type
  num_cache_nodes      = 1
  parameter_group_name = "default.redis7"
  port                 = 6379
  
  subnet_group_name    = aws_elasticache_subnet_group.this.name
  security_group_ids   = [aws_security_group.redis.id]

  # Maintenance window (low traffic time)
  maintenance_window = "sun:05:00-sun:06:00"

  tags = {
    Name        = "${var.service_name}-redis"
    Environment = var.environment
  }
}

