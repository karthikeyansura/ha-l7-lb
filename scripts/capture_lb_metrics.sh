#!/usr/bin/env bash
# Snapshot LB metrics and backend health during a run.
#
# Runs a curl script on the Locust EC2 (same VPC as LB tasks), pulling
# /metrics and /health/backends from EACH LB ECS task, then uploads the
# collected JSON to S3 and echoes a short summary.
#
# Usage:
#   ./scripts/capture_lb_metrics.sh <run_id>
#
# Example:
#   ./scripts/capture_lb_metrics.sh exp1/weighted_hetero_500u
#
# Call mid-run (to see active state) or post-run (pre-destroy) to capture
# per-backend request distribution.

set -euo pipefail

RUN_ID="${1:?run_id required}"

cd "$(dirname "$0")/../terraform"

INSTANCE_ID=$(terraform output -raw locust_instance_id)
BUCKET=$(terraform output -raw locust_results_bucket)
LB_CLUSTER=$(terraform output -raw lb_cluster_name)

S3_PREFIX="s3://${BUCKET}/${RUN_ID}"

# Shell script runs on the Locust EC2; it enumerates LB tasks and curls
# each. ECS task IPs come from `aws ecs describe-tasks` via the instance
# profile (SSMManaged + s3 is granted, but ECS describe is not — so we
# need to temporarily add permission OR drive the enumeration from the
# local machine).
#
# Safer path: drive enumeration locally (we have AWS creds); send a
# single per-IP curl script to the EC2.

TASK_ARNS=$(aws ecs list-tasks \
  --cluster "$LB_CLUSTER" \
  --desired-status RUNNING \
  --query "taskArns" --output text)

if [ -z "$TASK_ARNS" ] || [ "$TASK_ARNS" = "None" ]; then
  echo "[capture] no running LB tasks"
  exit 1
fi

# shellcheck disable=SC2086
TASK_IPS=$(aws ecs describe-tasks \
  --cluster "$LB_CLUSTER" \
  --tasks $TASK_ARNS \
  --query "tasks[].attachments[].details[?name=='privateIPv4Address'].value" \
  --output text | tr '\t' '\n' | sort -u)

echo "[capture] LB task IPs:"
echo "$TASK_IPS"

# Build remote curl commands.
CMDS="set -eux\nmkdir -p /tmp/capture\n"
i=0
for IP in $TASK_IPS; do
  CMDS+="curl -s --max-time 5 http://${IP}:9080/metrics           > /tmp/capture/lb${i}_metrics.json || true\n"
  CMDS+="curl -s --max-time 5 http://${IP}:9080/metrics/export    > /tmp/capture/lb${i}_timeseries.csv || true\n"
  CMDS+="curl -s --max-time 5 http://${IP}:9080/health/backends   > /tmp/capture/lb${i}_health.json || true\n"
  i=$((i + 1))
done
CMDS+="aws s3 sync /tmp/capture/ ${S3_PREFIX}/lb_snapshots/\n"

# shellcheck disable=SC2086
CMD_ID=$(aws ssm send-command \
  --instance-ids "$INSTANCE_ID" \
  --document-name "AWS-RunShellScript" \
  --comment "capture $RUN_ID" \
  --parameters "commands=[$(printf '%b' "$CMDS" | jq -Rs .)]" \
  --query "Command.CommandId" --output text)

for _ in $(seq 1 30); do
  STATUS=$(aws ssm list-command-invocations \
    --command-id "$CMD_ID" \
    --query "CommandInvocations[0].Status" \
    --output text 2>/dev/null || echo "Pending")
  case "$STATUS" in
    Success) echo "[capture] done: $RUN_ID"; exit 0 ;;
    Cancelled|TimedOut|Failed) echo "[capture] failed: $STATUS"; exit 1 ;;
  esac
  sleep 2
done

echo "[capture] timeout"
exit 2
