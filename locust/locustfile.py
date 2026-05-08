# Locust load test scenarios. Select a class via --class-picker or CLI.
# Point host at the NLB DNS name (AWS) or localhost:8080 (local).

import random
from locust import FastHttpUser, task, between


# Experiment 1: Algorithm comparison. Run under round-robin vs. least-connections
# vs. weighted policy and compare throughput/latency.
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

    # POST exercises the non-idempotent code path (no retry).
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


# Experiment 2: Chaos injection for retry efficacy evaluation.
# ~14% chaos (18:1:1:1 task ratio) avoids pool collapse while testing
# retry-on vs. retry-off delta.
class ChaosInjectionUser(FastHttpUser):
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

    # Chaos: force 500 from backend; LB should retry GET on a different backend.
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

    # Chaos: sleep 6-10s at backend, exceeding proxy's 5s timeout.
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


# Experiment 2a variant: same chaos semantics against /api/compute (CPU-bound).
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


# Experiment 3: Horizontal LB scaling. Sustained load to measure throughput
# scaling vs. lb_count. Run at 1, 2, 4, 8 LB instances behind the NLB.
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


# Backend stress test: mixed workload (CPU, data, payload, stream).
# 40% compute, 30% data, 20% payload, 10% stream.
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


# Experiment 3b: Scaling with CPU-bound workload (/api/compute).
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