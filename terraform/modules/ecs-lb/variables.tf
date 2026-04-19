variable "service_name" {
  type = string
}

variable "image" {
  type = string
}

variable "container_port" {
  type = number
}

variable "subnet_ids" {
  type = list(string)
}

variable "security_group_ids" {
  type = list(string)
}

variable "execution_role_arn" {
  type = string
}

variable "task_role_arn" {
  type = string
}

variable "log_group_name" {
  type = string
}

variable "ecs_count" {
  type    = number
  default = 2
}

variable "region" {
  type = string
}

variable "target_group_arn" {
  type = string
}

variable "redis_addr" {
  description = "ElastiCache endpoint injected as REDIS_ADDR env var."
  type        = string
}

variable "retries_enabled" {
  description = "Whether the LB retries failed idempotent requests. Injected as RETRIES_ENABLED env var; used by Experiment 2."
  type        = bool
  default     = true
}

variable "cpu" {
  type    = string
  default = "256"
}

variable "memory" {
  type    = string
  default = "512"
}
