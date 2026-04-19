#!/usr/bin/env bash
# Rebuild the ha-l7-lb-ops CloudWatch dashboard after terraform destroy.
# CloudWatch dashboards are free; the underlying ECS/ElastiCache/NLB metrics
# are retained for 15 days regardless of whether source resources still exist.
# This lets us capture per-run dashboards for runs that finished before the
# teardown.
#
# Usage: ./scripts/rebuild_dashboard.sh
set -euo pipefail

REGION="${AWS_REGION:-us-west-2}"
DASHBOARD_NAME="ha-l7-lb-ops"
NLB_ARN_SUFFIX="net/ha-l7-lb-nlb/7f5e20a86c2b0977"

BODY=$(cat <<JSON
{
  "widgets": [
    {
      "type": "metric", "x": 0, "y": 0, "width": 12, "height": 6,
      "properties": {
        "title": "LB ECS service - CPU & Memory",
        "view": "timeSeries", "stacked": false, "region": "${REGION}",
        "metrics": [
          ["AWS/ECS", "CPUUtilization", "ServiceName", "ha-l7-lb-lb", "ClusterName", "ha-l7-lb-lb-cluster"],
          [".", "MemoryUtilization", ".", ".", ".", "."]
        ],
        "period": 60, "stat": "Average",
        "yAxis": {"left": {"min": 0, "max": 100}}
      }
    },
    {
      "type": "metric", "x": 12, "y": 0, "width": 12, "height": 6,
      "properties": {
        "title": "Redis - CPU & Engine CPU",
        "view": "timeSeries", "stacked": false, "region": "${REGION}",
        "metrics": [
          ["AWS/ElastiCache", "CPUUtilization", "CacheClusterId", "ha-l7-lb-redis-001"],
          [".", "EngineCPUUtilization", ".", "."],
          [".", "NetworkBytesIn", ".", ".", {"yAxis": "right"}],
          [".", "NetworkBytesOut", ".", ".", {"yAxis": "right"}]
        ],
        "period": 60, "stat": "Average",
        "yAxis": {"left": {"min": 0, "max": 100}}
      }
    },
    {
      "type": "metric", "x": 0, "y": 6, "width": 12, "height": 6,
      "properties": {
        "title": "Backend strong - CPU & Memory",
        "view": "timeSeries", "stacked": false, "region": "${REGION}",
        "metrics": [
          ["AWS/ECS", "CPUUtilization", "ServiceName", "api-backend-strong", "ClusterName", "api-backend-strong-cluster"],
          [".", "MemoryUtilization", ".", ".", ".", "."]
        ],
        "period": 60, "stat": "Average",
        "yAxis": {"left": {"min": 0, "max": 100}}
      }
    },
    {
      "type": "metric", "x": 12, "y": 6, "width": 12, "height": 6,
      "properties": {
        "title": "Backend weak - CPU & Memory",
        "view": "timeSeries", "stacked": false, "region": "${REGION}",
        "metrics": [
          ["AWS/ECS", "CPUUtilization", "ServiceName", "api-backend-weak", "ClusterName", "api-backend-weak-cluster"],
          [".", "MemoryUtilization", ".", ".", ".", "."]
        ],
        "period": 60, "stat": "Average",
        "yAxis": {"left": {"min": 0, "max": 100}}
      }
    },
    {
      "type": "metric", "x": 0, "y": 12, "width": 12, "height": 6,
      "properties": {
        "title": "Backend homogeneous - CPU & Memory (exp2/exp3)",
        "view": "timeSeries", "stacked": false, "region": "${REGION}",
        "metrics": [
          ["AWS/ECS", "CPUUtilization", "ServiceName", "api-backend", "ClusterName", "api-backend-cluster"],
          [".", "MemoryUtilization", ".", ".", ".", "."]
        ],
        "period": 60, "stat": "Average",
        "yAxis": {"left": {"min": 0, "max": 100}}
      }
    },
    {
      "type": "metric", "x": 12, "y": 12, "width": 12, "height": 6,
      "properties": {
        "title": "NLB - Active & New flows, Processed Bytes",
        "view": "timeSeries", "stacked": false, "region": "${REGION}",
        "metrics": [
          ["AWS/NetworkELB", "ActiveFlowCount", "LoadBalancer", "${NLB_ARN_SUFFIX}"],
          [".", "NewFlowCount", ".", "."],
          [".", "ProcessedBytes", ".", ".", {"yAxis": "right"}]
        ],
        "period": 60, "stat": "Sum"
      }
    }
  ]
}
JSON
)

aws cloudwatch put-dashboard \
  --region "${REGION}" \
  --dashboard-name "${DASHBOARD_NAME}" \
  --dashboard-body "${BODY}"

echo
echo "Dashboard URL:"
echo "https://${REGION}.console.aws.amazon.com/cloudwatch/home?region=${REGION}#dashboards:name=${DASHBOARD_NAME}"
