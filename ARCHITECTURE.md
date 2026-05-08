# HA-L7-LB

## System Overview

```
Client → NLB (L4, TCP) → LB ECS tasks (L7, custom proxy) → Backend ECS tasks
                                  │
                            ElastiCache Redis
                         (health state coordination)
```

The system is a custom L7 load balancer deployed on AWS ECS Fargate behind a Network Load Balancer. It routes HTTP requests to a pool of backend servers using pluggable algorithms, performs active health checking, supports idempotent-method retry with a 20% retry budget, and coordinates health state across horizontally scaled LB instances via Redis Pub/Sub.

Backend servers are discovered dynamically through AWS Cloud Map (DNS-based service discovery). The infrastructure is managed with Terraform and load testing is driven by Locust on a dedicated EC2 instance via AWS SSM.

---

## Package Structure

```
cmd/
  lb/           Entry point for the load balancer process.
  backend/      Entry point for the backend server process.

internal/
  algorithms/   Pluggable routing strategies (Rule interface).
    RoundRobin.go           Stateless sequential distribution.
    LeastConnections.go     Power of Two Choices (randomized).
    Weighted.go             Decrementing counter pool.

  config/       YAML config + env var overrides (REDIS_ADDR, RETRIES_ENABLED).
  discovery/    DNS watcher for Cloud Map service discovery.
  health/       Active health checker + StatusUpdater interface.
  metrics/      Request-level and time-series metrics collection.
  proxy/        L7 reverse proxy with retry logic.
  repository/   Shared state abstraction and InMemory implementation.
    redismanager/   Redis Pub/Sub coordinator for multi-instance health sync.
```

---

## Concurrency Model

| Component | Synchronization | Notes |
|---|---|---|
| `InMemory.servers` | `sync.RWMutex` | RLock for reads (GetHealthy, GetAllServers), Lock for writes (MarkHealthy, SyncServers). |
| `ServerState.Healthy` | `atomic.Bool` | Lock-free reads from algorithms and health checker. |
| `ServerState.ActiveConnections` | `atomic.Int64` | Lock-free reads from LeastConnections; writes via `atomic.AddInt64`. |
| `Collector` | `sync.RWMutex` | Write lock for RecordRequest; read lock for GetSummary, ExportCSV. |
| `ReverseProxy.activeRequests` | `atomic.Int64` | Retry budget computation reads this without locking. |
| `Weighted.Weights` | `sync.RWMutex` | Full lock on GetTarget (both reads and writes to counters). |
| `RoundRobin.next` | `atomic.Uint64` | Lock-free counter increment. |

### Goroutine Ownership

- **Main goroutine**: HTTP server (blocking `ListenAndServe`).
- **Per-request goroutine**: created by `net/http` server; calls `ServeHTTP`.
- **Health checker**: single background goroutine, ticks at configured interval, spawns per-backend goroutines each cycle.
- **DNS watcher(s)**: one goroutine per configured backend endpoint (e.g., `api.internal`).
- **Redis Pub/Sub watcher**: single goroutine subscribing to the channel.
- **Periodic Redis sync**: single goroutine ticking every 30 seconds.
- **Time-series recorder**: single goroutine ticking every 5 seconds.

---

## Routing Algorithms

### Round Robin
Stateless sequential distribution using an atomic counter. O(1) per request. Provides uniform distribution regardless of backend load.

### Least Connections (Power of Two Choices)
Picks two random healthy backends and routes to the one with fewer active connections. O(1) per request. Provides near-optimal distribution even with local-only connection counters in a multi-LB deployment, avoiding the need for globally synchronized state.

### Weighted Round Robin
Distributes requests proportionally using a decrementing counter pool. Each backend holds `[originalWeight, remainingWeight]`. A random candidate is selected and its counter decremented. When all counters deplete, the pool resets. Supports heterogeneous backend tiers (e.g., strong 70% / weak 30%).

---

## Retry and Health Checking

### Request Lifecycle
1. Buffer the full request body into memory (required for retry replay).
2. Check for healthy backends (503 if none).
3. Select a backend via the configured algorithm.
4. Increment active connection counter.
5. Forward with a context timeout (default 5s).
6. On success: record metrics, return response.
7. On failure + idempotent method (GET, PUT, DELETE) + retries enabled:
   - Check retry budget (max 20% of in-flight requests may be retries).
   - Mark failed backend DOWN locally and propagate via Redis.
   - Select a different backend from remaining healthy set.
   - Retry once on the new backend.
8. On failure + non-idempotent method (POST, PATCH): return 504 immediately.

### Health Checker
- Probes every registered backend's `/health` endpoint on a fixed interval.
- A 200 OK within the timeout is healthy; anything else is DOWN.
- Only state transitions trigger updates (minimizes Redis write frequency).
- Draining backends are skipped.

### StatusUpdater Interface
Abstracts health state propagation. Implemented by `RedisManager` for cross-instance sync. The proxy and health checker call this interface without knowing about Redis.

---

## Redis Coordination

### Protocol
- **SET**: `backend:<URL>` → `"UP"` or `"DOWN"`. Persists state for startup-time reads.
- **PUBLISH**: `lb-backend-events` channel, message format `"URL|STATUS"`. Real-time propagation to running instances.

### Lifecycle
1. On startup, `SyncOnStartUp` reads all backend keys from Redis and applies to local state.
2. `StartPeriodicSync` ticks every 30 seconds, re-running `SyncOnStartUp` to heal missed Pub/Sub messages.
3. `StartRedisWatcher` subscribes to the Pub/Sub channel and applies incoming changes immediately.
4. On health state change, `UpdateBackendStatus` performs a two-phase write (SET + PUBLISH).

### Degraded Mode
If Redis is unavailable at startup, the LB runs with local-only health tracking. Each instance independently probes backends, potentially having inconsistent views until Redis recovers.

### Client Auto-Detection
`NewRedisManager` inspects the address string: if it contains commas, a Redis ClusterClient is created; otherwise, a single-node Client. This supports both single-node ElastiCache and multi-node clusters without code changes.

---

## DNS Discovery

Each configured backend endpoint spawns a DNS watcher goroutine that:
1. Resolves the hostname every 5 seconds via `net.DefaultResolver.LookupHost`.
2. Calls `SyncServersBySource` with a source tag scoping the update to avoid overwriting other sources' backends.
3. New IPs are added as healthy; removed IPs are drained (marked unhealthy, kept until active connections reach zero, then removed).

Multiple DNS sources (e.g., `api-strong.internal` and `api-weak.internal`) can coexist in one pool, each with its own weight, enabling heterogeneous backend tiers for weighted routing.

---

## Metrics and Observability

### Endpoints (port + 1000)
| Endpoint | Description |
|---|---|
| `GET /metrics` | JSON summary: total requests, failures, percentiles, per-backend breakdown. |
| `GET /metrics/timeseries` | JSON array of periodic snapshots (every 5s). |
| `GET /metrics/export` | CSV download of time-series data. |
| `GET /health/backends` | Current health and connection status of all backends. |

### Percentile Computation
Latencies are stored in a bounded slice (max 10,000 samples via reservoir sampling). Percentiles (p50, p95, p99) are computed on demand by sorting a copy — O(n log n) but only on the metrics endpoint, not the hot path.

### Graceful Shutdown
On SIGTERM (ECS task stop), the process drains in-flight connections (10s timeout), exports metrics to CSV, and exits. This ensures experiment data is not lost when scaling down LB instances.

---

## Infrastructure (Terraform)

### Module Map
| Module | Purpose |
|---|---|
| `network` | VPC, subnets, security groups. |
| `ecr_lb` / `ecr_backend` | ECR repositories for container images. |
| `logging` | CloudWatch log group. |
| `elasticache` | Redis (ElastiCache) for state coordination. |
| `nlb` | Network Load Balancer (L4 TCP). |
| `ecs-backend` | Backend ECS service + Cloud Map registration. |
| `ecs-lb` | LB ECS service + NLB target group attachment. |
| `autoscaling` | CPU-based auto-scaling for backend service. |
| `locust` | EC2 load generator + S3 results bucket. |

### Docker Build Triggers
Terraform hashes all tracked source files (Go source, config.yaml, Dockerfiles, go.mod/sum). Content changes force a fresh build and ECR push on `terraform apply`.

### Key Variables
- `lb_count`: Number of LB ECS tasks (Experiment 3 variable, default 2).
- `retries_enabled`: Toggle retry logic (Experiment 2, default true).
- `backend_min` / `backend_max`: Auto-scaling range for backend service.

---

## Experiment Design

### Experiment 1: Routing Algorithms
Compare round-robin, least-connections, and weighted on homogeneous and heterogeneous backends. Metrics: throughput (RPS), tail latency (p95/p99), per-backend request distribution.

### Experiment 2: Failure Isolation and Retry Efficacy
- **Part A**: Chaos injection via X-Chaos-Error (500s) and X-Chaos-Delay (exceeds proxy timeout). Compare client-observed error rates with retries on vs. off.
- **Part B**: Mid-run backend replica drop via `aws ecs update-service --desired-count`. Measures retry efficacy under narrow vs. broad failure scenarios.

### Experiment 3: Horizontal LB Scaling
Sweep `lb_count` in {1, 2, 4, 8} behind the NLB. Measure whether throughput scales linearly or if Redis coordination introduces contention. Tested on both `/api/data` (lightweight) and `/api/compute` (CPU-bound).

---

## Build and Run

### Local
```bash
go mod tidy
go build ./cmd/lb
go build ./cmd/backend
./backend -port 8080 &
./lb
```

### Docker
```bash
docker build -f Dockerfile.lb -t ha-l7-lb .
docker build -f Dockerfile.backend -t ha-l7-backend .
```

### AWS (Terraform)
```bash
cd terraform && terraform init && terraform apply
```

### Load Testing
```bash
./scripts/run_locust.sh exp1/rr_500u AlgorithmCompareUser 500 5
./scripts/capture_lb_metrics.sh exp1/rr_500u
```
