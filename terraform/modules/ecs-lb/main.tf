# ECS Fargate service for the L7 load balancer.
# Key difference from HW6's ECS module: injects REDIS_ADDR as an
# environment variable so the LB container connects to ElastiCache.
# Registers with the NLB target group (not ALB).

resource "aws_ecs_cluster" "this" { name = "${var.service_name}-cluster" }

resource "aws_ecs_task_definition" "this" {
  family                   = "${var.service_name}-task"
  network_mode             = "awsvpc"
  requires_compatibilities = ["FARGATE"]
  cpu                      = var.cpu
  memory                   = var.memory
  execution_role_arn       = var.execution_role_arn
  task_role_arn            = var.task_role_arn

  container_definitions = jsonencode([{
    name      = var.service_name
    image     = var.image
    essential = true
    portMappings = [{ containerPort = var.container_port }]

    # REDIS_ADDR env var overrides config.yaml's redis.addr field.
    # RETRIES_ENABLED env var overrides config.yaml's load_balancer.retries_enabled;
    # flipped to "false" during Experiment 2's retries-disabled variant.
    environment = [
      { name = "REDIS_ADDR", value = var.redis_addr },
      { name = "RETRIES_ENABLED", value = tostring(var.retries_enabled) },
    ]

    logConfiguration = {
      logDriver = "awslogs"
      options = {
        "awslogs-group"         = var.log_group_name
        "awslogs-region"        = var.region
        "awslogs-stream-prefix" = "ecs"
      }
    }
  }])
}

resource "aws_ecs_service" "this" {
  name            = var.service_name
  cluster         = aws_ecs_cluster.this.id
  task_definition = aws_ecs_task_definition.this.arn
  desired_count   = var.ecs_count
  launch_type     = "FARGATE"

  network_configuration {
    subnets          = var.subnet_ids
    security_groups  = var.security_group_ids
    assign_public_ip = true
  }

  load_balancer {
    target_group_arn = var.target_group_arn
    container_name   = var.service_name
    container_port   = var.container_port
  }

  health_check_grace_period_seconds = 60
  lifecycle { ignore_changes = [desired_count] }
}
