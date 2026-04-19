#!/usr/bin/env bash
# Run Locust for N minutes, drop one backend replica mid-run for Exp 2 Part B.
#
# Usage:
#   ./scripts/run_locust_with_replica_drop.sh <run_id> <user_class> <users> <duration_min> <drop_at_sec> <final_desired_count>
#
# Example:
#   ./scripts/run_locust_with_replica_drop.sh exp2b/retry_on_replicadrop ScalingBaselineUser 200 10 150 3
#
# This starts Locust in the background, waits `drop_at_sec` seconds,
# then executes `aws ecs update-service --desired-count N` to drop
# one backend replica during the run. The recorded stats_history.csv
# will contain the transition interval.

set -euo pipefail

RUN_ID="${1:?run_id required}"
USER_CLASS="${2:?user class required}"
USERS="${3:?user count required}"
DURATION_MIN="${4:?duration in minutes required}"
DROP_AT_SEC="${5:?seconds from start when to drop a replica required}"
FINAL_COUNT="${6:?final backend desired-count (e.g. 3) required}"

REPO_ROOT="$(dirname "$0")/.."

# Kick off Locust run in the background. run_locust.sh handles SSM dispatch
# and polls until the remote Locust command completes.
echo "[replica-drop] starting Locust at $(date +%H:%M:%S)"
(cd "$REPO_ROOT" && ./scripts/run_locust.sh "$RUN_ID" "$USER_CLASS" "$USERS" "$DURATION_MIN") &
LOCUST_PID=$!

# Wait until it's time to drop the replica.
echo "[replica-drop] waiting ${DROP_AT_SEC}s before drop"
sleep "$DROP_AT_SEC"

# Drop the replica.
echo "[replica-drop] scaling backend to $FINAL_COUNT at $(date +%H:%M:%S)"
aws ecs update-service \
  --cluster api-backend-cluster \
  --service api-backend \
  --desired-count "$FINAL_COUNT" \
  --query 'service.[runningCount,desiredCount]' --output text

# Wait for the Locust run to finish.
echo "[replica-drop] waiting for Locust to complete (pid=$LOCUST_PID)"
wait "$LOCUST_PID"
LOCUST_EXIT=$?

echo "[replica-drop] Locust exit: $LOCUST_EXIT at $(date +%H:%M:%S)"
exit "$LOCUST_EXIT"
