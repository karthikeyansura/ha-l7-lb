# Locust load test scenarios for the HA L7 Load Balancer.
# Each class corresponds to one of the three experiments.
# Select the desired class via the --class-picker flag in the Locust UI.
#
# Usage:
#   cd locust && docker compose up --scale worker=2
#   Open http://localhost:8089, select a class, set user count, and start.
#   Point the host field at the NLB DNS name (AWS) or localhost:8080 (local).

import random
from locust import FastHttpUser, task, between


# Experiment 1: Stateless vs. Stateful Routing Overhead.
#
# Run this class twice under identical conditions:
#   1. With config.yaml policy set to "round-robin" (stateless baseline).
#   2. With policy set to "least-connections" (stateful, Redis-backed).
# Compare throughput (RPS) and tail latency (p95/p99) between the two runs.
# Any degradation in the least-connections run is attributable to Redis
# state lookups on the routing hot path.
#
# Recommended: 100 to 2000 concurrent users, 3-5 minute runs.
class AlgorithmCompareUser(FastHttpUser):
    wait_time = between(0.05, 0.2)

    @task(8)
    def api_data(self):
        with self.client.get(
                "/api/data",
                name="/api/data",
                catch_response=True,
        ) as resp:
            if resp.status_code == 200:
                resp.success()
            else:
                resp.failure(f"Failed: {resp.status_code}")

    @task(1)
    def health_check(self):
        with self.client.get("/health", name="/health", catch_response=True) as resp:
            if resp.status_code == 200:
                resp.success()
            else:
                resp.failure(f"Health check failed: {resp.status_code}")

    # POST exercises the non-idempotent code path (no retry on failure).
    @task(1)
    def api_data_post(self):
        with self.client.post(
                "/api/data",
                json={"key": "value"},
                name="/api/data (POST)",
                catch_response=True,
        ) as resp:
            if resp.status_code == 200:
                resp.success()
            else:
                resp.failure(f"POST failed: {resp.status_code}")


# Experiment 2: Failure Isolation and Retry Efficacy under Chaos.
#
# Injects degradation via X-Chaos-Error (500 responses) and X-Chaos-Delay
# (multi-second latency exceeding the proxy's 2s timeout) headers.
#
# Run twice:
#   1. With retry logic enabled (default).
#   2. With retry logic disabled (modify isIdempotent to return false).
# Compare client-observed error rates and tail latencies.
#
# The 60/20/10/10 task ratio means ~60% normal, ~20% 500 errors,
# ~10% timeout-inducing delays, ~10% health checks.
#
# Recommended: 50 to 200 concurrent users, 3-5 minute runs.
class ChaosInjectionUser(FastHttpUser):
    wait_time = between(0.1, 0.5)

    @task(6)
    def normal_request(self):
        with self.client.get(
                "/api/data",
                name="/api/data (normal)",
                catch_response=True,
        ) as resp:
            if resp.status_code == 200:
                resp.success()
            else:
                resp.failure(f"Failed: {resp.status_code}")

    # Chaos: force a 500 from the backend. The LB should retry on a
    # different backend for GET requests, masking the error from the client.
    @task(2)
    def chaos_error_request(self):
        with self.client.get(
                "/api/data",
                headers={"X-Chaos-Error": "500"},
                name="/api/data (chaos-500)",
                catch_response=True,
        ) as resp:
            if resp.status_code == 200:
                resp.success()
            elif resp.status_code == 500:
                resp.failure("Backend 500 (expected chaos)")
            else:
                resp.failure(f"Unexpected: {resp.status_code}")

    # Chaos: sleep 3-10 seconds at the backend, exceeding the proxy's
    # 2-second timeout. Should trigger a retry on a healthy backend.
    @task(1)
    def chaos_delay_request(self):
        delay_ms = random.choice([3000, 5000, 10000])
        with self.client.get(
                "/api/data",
                headers={"X-Chaos-Delay": str(delay_ms)},
                name=f"/api/data (chaos-delay-{delay_ms}ms)",
                catch_response=True,
        ) as resp:
            if resp.status_code == 200:
                resp.success()
            else:
                resp.failure(f"Timeout/error: {resp.status_code}")

    @task(1)
    def health_check(self):
        with self.client.get("/health", name="/health", catch_response=True) as resp:
            if resp.status_code == 200:
                resp.success()
            else:
                resp.failure(f"Health check failed: {resp.status_code}")


# Experiment 3: Horizontal Scaling vs. State Contention.
#
# Baseline: run against a single LB instance until CPU saturation.
# Then scale to 2, 4, 8 LB instances behind the NLB (change lb_count
# in terraform/variables.tf and re-apply). Re-run with the same Locust
# parameters each time.
#
# Measure: if throughput scales linearly (2x instances = 2x RPS) or if
# the shared Redis layer introduces contention (sublinear scaling).
#
# ScalingBaselineUser provides sustained high load.
# ScalingSpikeUser provides extreme burst load to stress Redis contention.
#
# Recommended: 500+ concurrent users, ramp over 5 minutes.
class ScalingBaselineUser(FastHttpUser):
    wait_time = between(0.01, 0.05)

    @task(9)
    def api_data(self):
        with self.client.get(
                "/api/data",
                name="/api/data",
                catch_response=True,
        ) as resp:
            if resp.status_code == 200:
                resp.success()
            else:
                resp.failure(f"Failed: {resp.status_code}")

    @task(1)
    def health_check(self):
        with self.client.get("/health", name="/health", catch_response=True) as resp:
            if resp.status_code == 200:
                resp.success()
            else:
                resp.failure(f"Health check failed: {resp.status_code}")


class ScalingSpikeUser(FastHttpUser):
    """Extreme burst load to stress Redis contention at scale."""
    wait_time = between(0.001, 0.01)

    @task
    def api_data(self):
        with self.client.get(
                "/api/data",
                name="/api/data (spike)",
                catch_response=True,
        ) as resp:
            if resp.status_code == 200:
                resp.success()
            else:
                resp.failure(f"Failed: {resp.status_code}")
