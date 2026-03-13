variable "aws_region" {
  type    = string
  default = "us-east-1"
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

variable "backend_count" {
  description = "Number of backend ECS tasks."
  type        = number
  default     = 3
}

variable "backend_min" {
  description = "Minimum backend tasks for autoscaling."
  type        = number
  default     = 3
}

variable "backend_max" {
  description = "Maximum backend tasks for autoscaling."
  type        = number
  default     = 6
}

variable "cpu_target_value" {
  description = "CPU utilization target for backend autoscaling."
  type        = number
  default     = 70
}

variable "log_retention_days" {
  type    = number
  default = 7
}
