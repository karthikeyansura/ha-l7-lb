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
#   2. With policy set to "least-connections" (stateful).
# Compare throughput (RPS) and tail latency (p95/p99) between the two runs.
# Any degradation in the least-connections run is attributable to the
# connection-counting overhead on the routing hot path.
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
# (multi-second latency exceeding the proxy's timeout) headers.
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
    # Task weights dialed down per Sai's guidance: prior 6/2/1/1 (~30% chaos)
    # saturated the backend DOWN-marking and produced 99% 503 cascades that
    # masked the retry-on vs retry-off delta. Now 18/1/1/1 (~14% chaos)
    # keeps enough chaos to test retry efficacy without pool collapse.
    wait_time = between(0.1, 0.5)

    @task(18)
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
    @task(1)
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

    # Chaos: sleep 6-10 seconds at the backend, exceeding the proxy's
    # 5-second timeout. Should trigger a retry on a healthy backend.
    @task(1)
    def chaos_delay_request(self):
        delay_ms = random.choice([6000, 8000, 10000])
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


# Experiment 2 Part A CPU-heavy variant: same chaos semantics as
# ChaosInjectionUser but routed at /api/compute (CPU-bound ~10-30ms backend
# work per request with iterations=2000) instead of /api/data (5-25ms).
# Requires the backend to honor chaos headers on /api/compute too, which
# the handleChaos helper in cmd/backend/main.go provides.
class ChaosInjectionComputeUser(FastHttpUser):
    wait_time = between(0.1, 0.5)

    @task(18)
    def normal_request(self):
        with self.client.get(
                "/api/compute?iterations=2000",
                name="/api/compute (normal)",
                catch_response=True,
        ) as resp:
            if resp.status_code == 200:
                resp.success()
            else:
                resp.failure(f"Failed: {resp.status_code}")

    @task(1)
    def chaos_error_request(self):
        with self.client.get(
                "/api/compute?iterations=2000",
                headers={"X-Chaos-Error": "500"},
                name="/api/compute (chaos-500)",
                catch_response=True,
        ) as resp:
            if resp.status_code == 200:
                resp.success()
            elif resp.status_code == 500:
                resp.failure("Backend 500 (expected chaos)")
            else:
                resp.failure(f"Unexpected: {resp.status_code}")

    @task(1)
    def chaos_delay_request(self):
        delay_ms = random.choice([6000, 8000, 10000])
        with self.client.get(
                "/api/compute?iterations=2000",
                headers={"X-Chaos-Delay": str(delay_ms)},
                name=f"/api/compute (chaos-delay-{delay_ms}ms)",
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


# Backend Stress Test: exercises the heavier backend endpoints to prove
# the load balancer holds up under realistic workloads (CPU-bound compute,
# large payloads, long-lived streaming connections).
#
# Task mix: 40% compute (CPU), 30% lightweight data, 15% payload (bandwidth),
# 15% stream (connection holding).
#
# Recommended: 100 to 500 concurrent users, 3-5 minute runs.
class BackendStressUser(FastHttpUser):
    wait_time = between(0.05, 0.2)

    @task(4)
    def compute(self):
        with self.client.get(
                "/api/compute",
                name="/api/compute",
                catch_response=True,
        ) as resp:
            if resp.status_code == 200:
                resp.success()
            else:
                resp.failure(f"Compute failed: {resp.status_code}")

    @task(3)
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

    @task(2)
    def payload(self):
        with self.client.get(
                "/api/payload",
                name="/api/payload (1MB)",
                catch_response=True,
        ) as resp:
            if resp.status_code == 200:
                resp.success()
            else:
                resp.failure(f"Payload failed: {resp.status_code}")

    @task(1)
    def stream(self):
        with self.client.get(
                "/api/stream",
                name="/api/stream (chunked)",
                catch_response=True,
        ) as resp:
            if resp.status_code == 200:
                resp.success()
            else:
                resp.failure(f"Stream failed: {resp.status_code}")


# Experiment 3b: Horizontal Scaling with CPU-bound backend work.
#
# Same LB-count sweep as Experiment 3, but every request exercises the
# CPU-heavy /api/compute endpoint (~100-300ms of SHA-256 hashing per
# request). With heavy per-request backend work, the LB is no longer
# the obvious bottleneck -- backends share the load and Redis Pub/Sub
# coordination overhead becomes visible at higher LB counts.
class ScalingBaselineComputeUser(FastHttpUser):
    wait_time = between(0.01, 0.05)

    @task(9)
    def api_compute(self):
        with self.client.get(
                "/api/compute",
                name="/api/compute",
                catch_response=True,
        ) as resp:
            if resp.status_code == 200:
                resp.success()
            else:
                resp.failure(f"Compute failed: {resp.status_code}")

    @task(1)
    def health_check(self):
        with self.client.get("/health", name="/health", catch_response=True) as resp:
            if resp.status_code == 200:
                resp.success()
            else:
                resp.failure(f"Health check failed: {resp.status_code}")


class ScalingSpikeComputeUser(FastHttpUser):
    """Extreme burst load against /api/compute (CPU-bound)."""
    wait_time = between(0.001, 0.01)

    @task
    def api_compute(self):
        with self.client.get(
                "/api/compute",
                name="/api/compute (spike)",
                catch_response=True,
        ) as resp:
            if resp.status_code == 200:
                resp.success()
            else:
                resp.failure(f"Compute failed: {resp.status_code}")