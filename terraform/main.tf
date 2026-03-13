# Root module: composes all infrastructure from focused sub-modules.
#
# Architecture:
#   Client -> NLB (L4, TCP) -> LB ECS tasks (L7, custom proxy) -> Backend ECS tasks
#                                    |
#                              ElastiCache Redis (state coordination)
#
# Modules reused from HW6: ecr, logging, autoscaling (identical pattern).
# Modules new for this project: elasticache, nlb, ecs-lb, ecs-backend, network (extended).

# Networking: VPC, subnets, and three security groups (backend, LB, Redis).
module "network" {
  source         = "./modules/network"
  service_name   = var.service_name
  container_port = var.backend_port
  lb_port        = var.lb_port
  redis_port     = 6379
}

# Two ECR repositories: one for the LB image, one for the backend image.
module "ecr_lb" {
  source          = "./modules/ecr"
  repository_name = "${var.service_name}-lb"
}

module "ecr_backend" {
  source          = "./modules/ecr"
  repository_name = "${var.service_name}-backend"
}

# CloudWatch log group shared by both ECS services.
module "logging" {
  source            = "./modules/logging"
  service_name      = var.service_name
  retention_in_days = var.log_retention_days
}

# Existing IAM role created during HW2/HW5 setup.
data "aws_iam_role" "execution_role" {
  name = "ecsTaskExecutionRole"
}

# ElastiCache Redis: single-node instance for distributed LB state.
# The LB ECS tasks connect to this to share health status via Pub/Sub.
module "elasticache" {
  source            = "./modules/elasticache"
  service_name      = var.service_name
  subnet_ids        = module.network.subnet_ids
  security_group_id = module.network.redis_security_group_id
}

# Network Load Balancer (L4): the client-facing entry point.
# NLB is used instead of ALB because our custom LB IS the L7 layer;
# the AWS load balancer in front must operate at L4 (TCP passthrough).
module "nlb" {
  source       = "./modules/nlb"
  service_name = var.service_name
  vpc_id       = module.network.vpc_id
  subnet_ids   = module.network.subnet_ids
  lb_port      = var.lb_port
}

# ECS: backend service (no load balancer registration, no Redis).
module "ecs_backend" {
  source             = "./modules/ecs-backend"
  service_name       = "${var.service_name}-backend"
  image              = "${module.ecr_backend.repository_url}:latest"
  container_port     = var.backend_port
  subnet_ids         = module.network.subnet_ids
  security_group_ids = [module.network.backend_security_group_id]
  execution_role_arn = data.aws_iam_role.execution_role.arn
  task_role_arn      = data.aws_iam_role.execution_role.arn
  log_group_name     = module.logging.log_group_name
  ecs_count          = var.backend_count
  region             = var.aws_region
}

# ECS: LB service. Registers with NLB target group.
# REDIS_ADDR env var is injected so the LB container connects to ElastiCache.
module "ecs_lb" {
  source             = "./modules/ecs-lb"
  service_name       = "${var.service_name}-lb"
  image              = "${module.ecr_lb.repository_url}:latest"
  container_port     = var.lb_port
  subnet_ids         = module.network.subnet_ids
  security_group_ids = [module.network.lb_security_group_id]
  execution_role_arn = data.aws_iam_role.execution_role.arn
  task_role_arn      = data.aws_iam_role.execution_role.arn
  log_group_name     = module.logging.log_group_name
  ecs_count          = var.lb_count
  region             = var.aws_region
  target_group_arn   = module.nlb.target_group_arn
  redis_addr         = module.elasticache.redis_endpoint
}

# Autoscaling: backend service only. LB scaling is manual (Experiment 3).
module "autoscaling_backend" {
  source           = "./modules/autoscaling"
  service_name     = "${var.service_name}-backend"
  ecs_cluster_name = module.ecs_backend.cluster_name
  ecs_service_name = module.ecs_backend.service_name
  min_capacity     = var.backend_min
  max_capacity     = var.backend_max
  cpu_target_value = var.cpu_target_value
}

# Build and push Docker images as part of terraform apply.
resource "docker_image" "lb" {
  name = "${module.ecr_lb.repository_url}:latest"
  build {
    context    = "../"
    dockerfile = "Dockerfile.lb"
  }
}

resource "docker_registry_image" "lb" {
  name = docker_image.lb.name
}

resource "docker_image" "backend" {
  name = "${module.ecr_backend.repository_url}:latest"
  build {
    context    = "../"
    dockerfile = "Dockerfile.backend"
  }
}

resource "docker_registry_image" "backend" {
  name = docker_image.backend.name
}
