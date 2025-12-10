output "redis_endpoint" {
  description = "Redis cluster endpoint address"
  value       = aws_elasticache_cluster.this.cache_nodes[0].address
}

output "redis_port" {
  description = "Redis port"
  value       = aws_elasticache_cluster.this.port
}

output "redis_connection_string" {
  description = "Full Redis connection string"
  value       = "${aws_elasticache_cluster.this.cache_nodes[0].address}:${aws_elasticache_cluster.this.port}"
}

output "security_group_id" {
  description = "Redis security group ID"
  value       = aws_security_group.redis.id
}

