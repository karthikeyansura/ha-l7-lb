# CloudWatch dashboard covering all three experiments. Set the time range
# to the run window before screenshotting. Panels: LB CPU/memory,
# Backend strong/weak/homogeneous CPU/memory, Redis CPU/network, NLB flows.

resource "aws_cloudwatch_dashboard" "ops" {
  dashboard_name = "${var.service_name}-ops"

  dashboard_body = jsonencode({
    widgets = [
      {
        type   = "metric"
        x      = 0
        y      = 0
        width  = 12
        height = 6
        properties = {
          title   = "LB ECS service — CPU & Memory"
          view    = "timeSeries"
          stacked = false
          region  = var.aws_region
          metrics = [
            ["AWS/ECS", "CPUUtilization", "ServiceName", "${var.service_name}-lb", "ClusterName", "${var.service_name}-lb-cluster"],
            [".", "MemoryUtilization", ".", ".", ".", "."],
          ]
          period = 60
          stat   = "Average"
          yAxis = {
            left = { min = 0, max = 100 }
          }
        }
      },
      {
        type   = "metric"
        x      = 12
        y      = 0
        width  = 12
        height = 6
        properties = {
          title   = "Redis — CPU & Engine CPU"
          view    = "timeSeries"
          stacked = false
          region  = var.aws_region
          metrics = [
            ["AWS/ElastiCache", "CPUUtilization", "CacheClusterId", "${var.service_name}-redis-001"],
            [".", "EngineCPUUtilization", ".", "."],
            [".", "NetworkBytesIn", ".", ".", { yAxis = "right" }],
            [".", "NetworkBytesOut", ".", ".", { yAxis = "right" }],
          ]
          period = 60
          stat   = "Average"
          yAxis = {
            left = { min = 0, max = 100 }
          }
        }
      },
      {
        type   = "metric"
        x      = 0
        y      = 6
        width  = 12
        height = 6
        properties = {
          title   = "Backend strong — CPU & Memory"
          view    = "timeSeries"
          stacked = false
          region  = var.aws_region
          metrics = [
            ["AWS/ECS", "CPUUtilization", "ServiceName", "api-backend-strong", "ClusterName", "api-backend-strong-cluster"],
            [".", "MemoryUtilization", ".", ".", ".", "."],
          ]
          period = 60
          stat   = "Average"
          yAxis = {
            left = { min = 0, max = 100 }
          }
        }
      },
      {
        type   = "metric"
        x      = 12
        y      = 6
        width  = 12
        height = 6
        properties = {
          title   = "Backend weak — CPU & Memory"
          view    = "timeSeries"
          stacked = false
          region  = var.aws_region
          metrics = [
            ["AWS/ECS", "CPUUtilization", "ServiceName", "api-backend-weak", "ClusterName", "api-backend-weak-cluster"],
            [".", "MemoryUtilization", ".", ".", ".", "."],
          ]
          period = 60
          stat   = "Average"
          yAxis = {
            left = { min = 0, max = 100 }
          }
        }
      },
      {
        type   = "metric"
        x      = 0
        y      = 12
        width  = 12
        height = 6
        properties = {
          title   = "Backend homogeneous — CPU & Memory (exp2/exp3)"
          view    = "timeSeries"
          stacked = false
          region  = var.aws_region
          metrics = [
            ["AWS/ECS", "CPUUtilization", "ServiceName", "api-backend", "ClusterName", "api-backend-cluster"],
            [".", "MemoryUtilization", ".", ".", ".", "."],
          ]
          period = 60
          stat   = "Average"
          yAxis = {
            left = { min = 0, max = 100 }
          }
        }
      },
      {
        type   = "metric"
        x      = 12
        y      = 12
        width  = 12
        height = 6
        properties = {
          title   = "NLB — Active & New flows, Processed Bytes"
          view    = "timeSeries"
          stacked = false
          region  = var.aws_region
          metrics = [
            ["AWS/NetworkELB", "ActiveFlowCount", "LoadBalancer", module.nlb.nlb_arn_suffix],
            [".", "NewFlowCount", ".", "."],
            [".", "ProcessedBytes", ".", ".", { yAxis = "right" }],
          ]
          period = 60
          stat   = "Sum"
        }
      },
    ]
  })
}

output "dashboard_url" {
  description = "CloudWatch dashboard with all panels — set time range to the run window and screenshot."
  value       = "https://${var.aws_region}.console.aws.amazon.com/cloudwatch/home?region=${var.aws_region}#dashboards:name=${aws_cloudwatch_dashboard.ops.dashboard_name}"
}
