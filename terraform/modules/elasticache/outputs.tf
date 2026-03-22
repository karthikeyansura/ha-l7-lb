# Outputs the primary endpoint address as "address:port" for use as REDIS_ADDR.
# During a failover, AWS automatically remaps this DNS endpoint to the new primary.
output "redis_endpoint" {
  value = "${aws_elasticache_replication_group.this.primary_endpoint_address}:${aws_elasticache_replication_group.this.port}"
}
