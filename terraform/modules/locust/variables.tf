variable "service_name" {
  description = "Prefix for all resource names."
  type        = string
}

variable "vpc_id" {
  description = "VPC in which to place the Locust EC2 instance."
  type        = string
}

variable "subnet_id" {
  description = "Public subnet (auto-assign public IP) for the Locust EC2."
  type        = string
}

variable "nlb_dns_name" {
  description = "NLB DNS name that Locust targets as its host."
  type        = string
}

variable "lb_security_group_id" {
  description = "LB ECS task security group id. Locust SG is granted ingress on the LB metrics port so it can scrape /metrics."
  type        = string
}

variable "lb_metrics_port" {
  description = "Port where the LB exposes /metrics, /health/backends, etc."
  type        = number
  default     = 9080
}

variable "instance_type" {
  description = "EC2 instance type for the load generator."
  type        = string
  default     = "c6i.xlarge"
}

variable "locustfile_path" {
  description = "Relative path from the terraform root to the locustfile."
  type        = string
  default     = "../locust/locustfile.py"
}
