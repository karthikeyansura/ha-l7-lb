# Network module: uses the default VPC and creates three security groups
# implementing a layered access model:
#   NLB (public) -> LB SG -> Backend SG
#                   LB SG -> Redis SG
# Backends and Redis are not directly accessible from the internet.

data "aws_vpc" "default" {
  default = true
}

data "aws_subnets" "default" {
  filter {
    name   = "vpc-id"
    values = [data.aws_vpc.default.id]
  }
}

# Backend SG: accepts traffic only from LB tasks on the backend port.
resource "aws_security_group" "backend" {
  name        = "${var.service_name}-backend-sg"
  description = "Allow inbound from LB on ${var.container_port}"
  vpc_id      = data.aws_vpc.default.id

  ingress {
    from_port       = var.container_port
    to_port         = var.container_port
    protocol        = "tcp"
    security_groups = [aws_security_group.lb.id]
    description     = "Allow traffic from LB tasks"
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

# LB SG: accepts traffic from NLB (public) on the LB port.
# NLB preserves client IPs, so the CIDR must be 0.0.0.0/0.
resource "aws_security_group" "lb" {
  name        = "${var.service_name}-lb-sg"
  description = "Allow inbound from NLB on ${var.lb_port}"
  vpc_id      = data.aws_vpc.default.id

  ingress {
    from_port   = var.lb_port
    to_port     = var.lb_port
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
    description = "Allow traffic from NLB"
  }

  # LB metrics endpoint (port+1000). VPC-internal only. Used by
  # capture_lb_metrics.sh via SSM from the Locust EC2 in the same VPC.
  ingress {
    from_port   = var.lb_port + 1000
    to_port     = var.lb_port + 1000
    protocol    = "tcp"
    cidr_blocks = [data.aws_vpc.default.cidr_block]
    description = "Allow VPC-internal access to LB metrics endpoint"
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

# Redis SG: accepts traffic only from LB tasks on port 6379.
resource "aws_security_group" "redis" {
  name        = "${var.service_name}-redis-sg"
  description = "Allow inbound from LB tasks on ${var.redis_port}"
  vpc_id      = data.aws_vpc.default.id

  ingress {
    from_port       = var.redis_port
    to_port         = var.redis_port
    protocol        = "tcp"
    security_groups = [aws_security_group.lb.id]
    description     = "Allow traffic from LB tasks"
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}