#!/usr/bin/env bash
# Drive a single Locust run on the AWS-hosted load generator via SSM.
#
# Usage:
#   ./scripts/run_locust.sh <run_id> <user_class> <users> <duration_min> [spawn_rate]
#
# Examples:
#   ./scripts/run_locust.sh exp1/weighted_hetero_500u AlgorithmCompareUser 500 5
#   ./scripts/run_locust.sh exp2/retry_on_100u       ChaosInjectionUser   100 5
#   ./scripts/run_locust.sh exp3/lb1_2000u_spike     ScalingSpikeUser     2000 3
#
# Assumes terraform outputs are populated and Locust EC2 has been
# bootstrapped (locust in /usr/local/bin, locustfile at /opt/locust/).
#
# Uses aws ssm send-command --cli-input-json (via a generated JSON payload)
# so multi-line shell commands preserve real newlines. The AWS CLI's
# shorthand --parameters syntax collapses escapes and mangles heredocs.

set -euo pipefail

RUN_ID="${1:?run_id required (e.g. exp1/weighted_hetero_500u)}"
USER_CLASS="${2:?user class required}"
USERS="${3:?user count required}"
DURATION_MIN="${4:?duration in minutes required}"
SPAWN_RATE="${5:-$((USERS / 10))}"

cd "$(dirname "$0")/../terraform"

INSTANCE_ID=$(terraform output -raw locust_instance_id)
NLB_DNS=$(terraform output -raw nlb_dns_name)
BUCKET=$(terraform output -raw locust_results_bucket)

cd - > /dev/null

RUN_SLUG="${RUN_ID//\//_}"
S3_PREFIX="s3://${BUCKET}/${RUN_ID}"

echo "[run_locust] $RUN_ID -> nlb=$NLB_DNS users=$USERS rate=$SPAWN_RATE dur=${DURATION_MIN}m"

# Compose the shell script that will run on the EC2.
# Locust can exit non-zero under chaos (failures ARE the Exp 2 signal).
# Treat locust failure as soft — still upload artifacts if they exist.
# Fail hard only if locust didn't produce any CSVs at all.
read -r -d '' CMD <<EOF || true
set -uo pipefail
cd /opt/locust
rm -f run-*.csv run-*.html
locust --headless \
  -u ${USERS} -r ${SPAWN_RATE} -t ${DURATION_MIN}m \
  --host http://${NLB_DNS} \
  --csv run-${RUN_SLUG} \
  --html run-${RUN_SLUG}.html \
  --only-summary \
  -f locustfile.py \
  ${USER_CLASS} || true
[ -f run-${RUN_SLUG}_stats.csv ] || { echo "locust produced no stats.csv"; exit 1; }
aws s3 cp run-${RUN_SLUG}_stats.csv         ${S3_PREFIX}/stats.csv
aws s3 cp run-${RUN_SLUG}_stats_history.csv ${S3_PREFIX}/stats_history.csv || true
aws s3 cp run-${RUN_SLUG}_failures.csv      ${S3_PREFIX}/failures.csv || true
aws s3 cp run-${RUN_SLUG}.html              ${S3_PREFIX}/report.html || true
EOF

# Build the SSM payload using jq so newlines are correctly JSON-escaped.
PAYLOAD=$(mktemp /tmp/ssm-run-XXXXXX.json)
trap "rm -f $PAYLOAD" EXIT

jq -n \
  --arg iid "$INSTANCE_ID" \
  --arg cmt "$RUN_ID" \
  --arg cmd "$CMD" \
  '{
    InstanceIds: [$iid],
    DocumentName: "AWS-RunShellScript",
    Comment: $cmt,
    Parameters: {commands: [$cmd]},
    CloudWatchOutputConfig: {CloudWatchOutputEnabled: true}
  }' > "$PAYLOAD"

CMD_ID=$(aws ssm send-command --cli-input-json "file://$PAYLOAD" --query "Command.CommandId" --output text)

echo "[run_locust] SSM command id: $CMD_ID"
echo "[run_locust] polling every 10s..."

# Run should take ${DURATION_MIN} minutes + ramp + upload.
MAX_WAIT=$((DURATION_MIN * 6 + 30))
for _ in $(seq 1 "$MAX_WAIT"); do
  STATUS=$(aws ssm list-command-invocations \
    --command-id "$CMD_ID" \
    --query "CommandInvocations[0].Status" \
    --output text 2>/dev/null || echo "Pending")

  case "$STATUS" in
    Success)
      echo "[run_locust] completed: $RUN_ID"
      exit 0
      ;;
    Cancelled|TimedOut|Failed)
      echo "[run_locust] failed: $STATUS"
      aws ssm get-command-invocation \
        --command-id "$CMD_ID" \
        --instance-id "$INSTANCE_ID" \
        --query "StandardErrorContent" --output text | tail -30
      echo "--- stdout ---"
      aws ssm get-command-invocation \
        --command-id "$CMD_ID" \
        --instance-id "$INSTANCE_ID" \
        --query "StandardOutputContent" --output text | tail -30
      exit 1
      ;;
  esac
  sleep 10
done

echo "[run_locust] timeout waiting for command"
exit 2
