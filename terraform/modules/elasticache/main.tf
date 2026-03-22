# ElastiCache Redis Replication Group for distributed LB state coordination.
# Multi-AZ setup with 2 nodes ensures high availability and failover support.
# The LB's RedisManager auto-detects single-node vs. cluster mode.

resource "aws_elasticache_subnet_group" "this" {
  name       = "${var.service_name}-redis-subnet"
  subnet_ids = var.subnet_ids
}

resource "aws_elasticache_replication_group" "this" {
  replication_group_id       = "${var.service_name}-redis"
  description                = "Multi-AZ Redis for LB coordination"
  engine                     = "redis"
  node_type                  = "cache.t3.micro"
  num_cache_clusters         = 2
  parameter_group_name       = "default.redis7"
  port                       = 6379
  security_group_ids         = [var.security_group_id]
  subnet_group_name          = aws_elasticache_subnet_group.this.name

  # Enable High Availability features
  automatic_failover_enabled = true
  multi_az_enabled           = true
}
