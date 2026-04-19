variable "aws_region" {
  type    = string
  default = "us-west-2"
}

variable "service_name" {
  type    = string
  default = "ha-l7-lb"
}

variable "lb_port" {
  description = "Port the LB container listens on."
  type        = number
  default     = 8080
}

variable "backend_port" {
  description = "Port the backend container listens on."
  type        = number
  default     = 8080
}

# Experiment 3: change this to 1, 2, 4, or 8 and re-apply to test
# horizontal LB scaling behind the NLB.
variable "lb_count" {
  description = "Number of LB ECS tasks (Experiment 3 variable)."
  type        = number
  default     = 2
}

variable "backend_min" {
  type = number
  default = 2
}

variable "backend_max" {
  type = number
  default = 8
}

variable "cpu_target_value" {
  type = number
  default = 70
}

variable "log_retention_days" {
  type    = number
  default = 7
}

# Experiment 2: flip to false via `terraform apply -var=retries_enabled=false`
# to run the retries-disabled variant without rebuilding the LB image.
variable "retries_enabled" {
  description = "Whether the LB retries failed idempotent requests. Experiment 2 toggle."
  type        = bool
  default     = true
}

# Experiment 1 dual-tier backend topology.
# On this branch, ecs_backend is split into strong + weak tiers registered
# under separate Cloud Map services (api-strong.internal, api-weak.internal).

variable "backend_strong_count" {
  type    = number
  default = 1
}

variable "backend_weak_count" {
  type    = number
  default = 1
}

variable "backend_strong_cpu" {
  type    = string
  default = "512"
}

variable "backend_strong_memory" {
  type    = string
  default = "1024"
}

variable "backend_weak_cpu" {
  type    = string
  default = "256"
}

variable "backend_weak_memory" {
  type    = string
  default = "512"
}