# Exp 3b — Compute-heavy scaling (attempted, inconclusive)

Follow-up to Exp 3 hypothesizing how scaling changes when backends do
real CPU work (/api/compute, ~100-300ms SHA-256 hashing per request)
instead of the trivial /api/data (~5-25ms).

## Runs attempted

| Run | Users | retries_enabled | Outcome |
|-----|-------|-----------------|---------|
| lb1_u500_compute   | 500 | true  | 99.94% failure, backends marked DOWN via retry cascade |
| lb1_u500_compute   | 500 | false | 99.98% failure, backends marked DOWN via health-check timeout |
| lb1_u100_compute   | 100 | false | 94.30% failure, same cascade mechanism |

## Why the runs did not complete with clean numbers

`ScalingBaselineComputeUser` generates requests at ~1/wait_time per user.
At wait_time `[0.01, 0.05]` and u=100 that's ~2000-10000 req/s theoretical,
vastly exceeding the backend capacity of ~5-10 req/s per 256-CPU Fargate
task. With the health-check probe sharing the same saturated backend,
`/health` also times out and the health checker marks backends DOWN.
Result: the LB returns `503 No healthy backends` for most traffic.

## Findings (qualitative)

1. **Compute workload saturates backends, not LB.** Exp 3's LB-bound
   scaling curve does not hold for `/api/compute`: backends are the
   bottleneck by two orders of magnitude.
2. **Health-check timeout under overload is a second cascade path.**
   Even with `retries_enabled=false` (which disables the
   retry-triggered DOWN marking), the health checker path still marks
   backends DOWN when their `/health` reply exceeds the 5 s timeout
   while queued behind saturated compute work.
3. **Disabling retries is necessary but not sufficient** to avoid
   backend-DOWN cascades under persistent overload. A complete fix
   requires (a) dedicated health-check capacity on the backend
   (e.g. a thread pool that isn't shared with request handling) and/or
   (b) graduated health policy (N consecutive failures, cool-down,
   half-open probe).

## Not captured (future work)

A cleaner Exp 3b would require one of:
- Run with u=20 or smaller (matches backend capacity)
- Send `/api/compute?iterations=1000` to reduce per-request cost
- Increase `health_check.timeout` beyond the backend's p99 under load
- Move the backend health-check path to a separate goroutine/pool

With those in place the LB-count scaling curve for compute could be
measured cleanly. The Exp 3 /api/data results remain the authoritative
scaling measurement for this report.
