# HA-L7-LB

High-Availability Layer 7 Load Balancer with Distributed State Coordination.

Custom L7 reverse proxy in Go with pluggable routing algorithms, active health checking, idempotent-method retry logic with a 20% retry budget, and Redis Pub/Sub coordination across horizontally scaled LB instances. Deployed on AWS ECS Fargate via Terraform (NLB, ElastiCache, Cloud Map discovery). Empirical results and full methodology are documented in `docs/final_report.md`; artifacts live under `results/`.

## Architecture

```
Client -> NLB (L4) -> LB ECS tasks (L7) -> Backend ECS tasks
                           |
                     ElastiCache Redis
```

The load balancer discovers backends through Cloud Map (per-endpoint DNS watchers; multiple sources and weights can share one pool for heterogeneous tiers). Health state is synchronized across LB tasks over Redis; the proxy can run in a degraded mode with local-only health if Redis is unavailable. The backend exposes `/api/data`, `/api/compute`, `/api/payload`, and `/api/stream` for experiments, with optional chaos via `X-Chaos-Error` and `X-Chaos-Delay` headers. Observability: `/metrics`, `/metrics/timeseries`, `/metrics/export` (CSV), and `/health/backends` on port+1000; see the final report for details.

## Algorithms

- **Round robin** — stateless sequential distribution; O(1) per request.
- **Least connections** — power-of-two choices: two random candidates, route to the one with fewer in-flight requests; O(1) per request.
- **Weighted** — configurable traffic proportion per backend; supports multi-tier discovery with per-endpoint weights.

## Retries and health

- Idempotent methods (GET, PUT, DELETE) may be retried on a different backend after 5xx or proxy timeout, with a 20% in-flight retry budget, optional `retries_enabled` (config / env), and no retry on POST/PATCH.
- Health checks probe `/health` on a fixed interval; failed probes mark backends DOWN; state is shared across LBs via Redis.

## Experiments

1. **Routing algorithms** — Compare round-robin, least-connections, and weighted on homogeneous and heterogeneous backends: `AlgorithmCompareUser` on `/api/data` (milestone 1 and final weighted 70/30 run) and a `BackendStressUser` mixed workload (compute, data, large payload, stream) in a 2×3 homo/hetero × policy matrix under `results/final/`.
2. **Failure isolation and retry efficacy** — Chaos injection (Locust; low-chaos ~10% in `exp2a_*`, higher 30% contrast in `exp2/`) and mid-run replica reduction (`exp2b/`), retry on vs off. Outcome depends on failure breadth (see `docs/final_report.md` §4).
3. **Horizontal LB scaling** — `lb_count` in {1, 2, 4, 8} with `ScalingBaselineUser` / `ScalingSpikeUser` on `/api/data` and a compute-bound variant on `/api/compute` (`exp3/`, `exp3_compute/`). Overload/health-probe interaction is documented in `results/final/exp3b/`.


Regenerated figures: `results/figures/` (see `scripts/gen_report_figures.py`). Extended narrative: `docs/final_report.md`. Course planning doc: `docs/plan.md`.

## Setup

```bash
go mod tidy
go build ./cmd/lb
go build ./cmd/backend
docker build -f Dockerfile.lb -t ha-l7-lb .
docker build -f Dockerfile.backend -t ha-l7-backend .
cd terraform && terraform init && terraform apply
```
