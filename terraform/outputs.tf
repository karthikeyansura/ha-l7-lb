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

output "lb_cluster_name" {
  value = module.ecs_lb.cluster_name
}

output "backend_cluster_name" {
  value = module.ecs_backend.cluster_name
}

# Locust load generator — drive runs via `aws ssm send-command --instance-ids`
# and pull artifacts from the results bucket.
output "locust_instance_id" {
  description = "EC2 id for the Locust load generator. Use with aws ssm send-command."
  value       = module.locust.instance_id
}

output "locust_public_ip" {
  description = "Locust EC2 public IP (informational; SSH not enabled)."
  value       = module.locust.public_ip
}

output "locust_results_bucket" {
  description = "S3 bucket where Locust artifacts land."
  value       = module.locust.s3_bucket
}

output "aws_region" {
  description = "AWS region (echoes the variable for scripts that need it)."
  value       = var.aws_region
}
