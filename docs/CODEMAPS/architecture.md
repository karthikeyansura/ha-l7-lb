<!-- Generated: 2026-03-29 | Files scanned: 22 | Token estimate: ~650 | Last commit: c1d741e -->
# Architecture — HA L7 Load Balancer

## System Diagram

```
Client ──► NLB (L4) ──► LB ECS Tasks (L7) ──► Backend ECS Tasks
                              │
                        ElastiCache Redis
                        (Pub/Sub state sync)
                              │
                        Cloud Map DNS
                        (service discovery, 5s poll)
```

## Package Map

```
cmd/
  lb/main.go         (283 lines) — entry point, wiring, metrics HTTP server, graceful shutdown
  backend/main.go    (110 lines) — test backend with chaos injection headers

internal/
  config/config.go   (101 lines) — YAML + env override (REDIS_ADDR, REDIS_PASSWORD)
  proxy/proxy.go     (352 lines) — L7 reverse proxy, body buffering, retry budget, idempotent retry
  algorithms/        — pluggable routing: RoundRobin, LeastConnections (P2C), Weighted
  repository/        — SharedState interface + InMemory impl + ServerState model (with draining)
    redismanager/    — Redis Pub/Sub coordinator, startup sync, periodic re-sync
  health/            — StatusUpdater interface + active Checker (periodic /health probes)
  metrics/           — Collector: reservoir-sampled latencies, percentiles, per-backend stats, CSV export
  discovery/         — DNS watcher: polls Cloud Map, calls pool.SyncServers()
```

Total: ~3,310 lines Go (~1,230 test lines).

## Four Core Interfaces

| Interface | Package | Implementations |
|-----------|---------|-----------------|
| `SharedState` | repository | `InMemory` (sole) |
| `Rule` | algorithms | `RoundRobin`, `LeastConnectionsPolicy`, `Weighted` |
| `StatusUpdater` | health | `RedisManager` (or nil in degraded mode) |
| `CollectMetrics` | metrics | `Collector` |

## Startup Sequence

1. Load config (YAML + env overrides)
2. Select routing algorithm from policy string
3. Create empty InMemory pool
4. Start DNS watcher → populates pool from Cloud Map
5. Connect Redis (optional, warns on failure → degraded mode)
6. Sync local state from Redis + start periodic re-sync + Pub/Sub watcher
7. Start health checker (concurrent per-backend probes)
8. Start metrics time-series recorder (5s ticks)
9. Start metrics HTTP server (port+1000)
10. Register graceful shutdown (SIGTERM → drain connections 10s → dump metrics to disk)
11. Start main HTTP server (foreground, blocks)

## Concurrency Model

- `ServerState.Healthy`: `atomic.Bool` — lock-free reads
- `ServerState.ActiveConnections`: `atomic.Int64` — lock-free reads
- `ServerState.Draining`: `atomic.Bool` — lock-free reads (connection draining state)
- `InMemory.mu`: `sync.RWMutex` — protects servers slice
- `Collector.mu`: `sync.RWMutex` — serializes metric writes
- `Weighted.mu`: `sync.RWMutex` — protects weight counters
- `RoundRobin.next`: `atomic.Uint64` — lock-free counter
- `ReverseProxy.activeRequests/activeRetries`: `atomic.Int64` — retry budget tracking

## Degraded Mode

Redis is optional. If unavailable at startup, LB runs with local-only health state. The `StatusUpdater` and proxy both nil-check before calling Redis.
