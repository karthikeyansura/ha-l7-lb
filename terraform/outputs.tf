# The NLB DNS name is the client entry point for all experiments.
# Point Locust's host field at this value.
output "nlb_dns_name" {
  description = "DNS name of the Network Load Balancer."
  value       = module.nlb.nlb_dns_name
}

output "redis_endpoint" {
  description = "ElastiCache Redis endpoint for LB coordination."
  value       = module.elasticache.redis_endpoint
}

output "backend_cluster_name" {
  value = module.ecs_backend.cluster_name
}

output "lb_cluster_name" {
  value = module.ecs_lb.cluster_name
}
