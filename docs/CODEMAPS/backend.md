<!-- Generated: 2026-03-29 | Files scanned: 22 | Token estimate: ~850 | Last commit: c1d741e -->
# Backend Architecture

## Proxy Request Flow

```
ServeHTTP(w, r)
  ├─ atomic.Add(activeRequests, 1)
  ├─ MaxBytesReader(r.Body, 10MB) → reject oversized payloads (413)
  ├─ io.ReadAll(r.Body) → buffer for retry replay
  ├─ pool.GetHealthy() → early 503 if empty
  ├─ algo.GetTarget(pool, r) → url.URL
  ├─ pool.AddConnections(url, 1)
  ├─ proxyRequest(w, r, url) — configurable timeout via config.yaml
  ├─ pool.RemoveConnections(url, 1)
  ├─ on success → RecordRequest(success=true)
  ├─ on client disconnect → RecordRequest(success=false), NO retry, NO DOWN mark
  ├─ on backend failure → retry path (if idempotent)
  └─ on 5xx response → treated as backend error, triggers retry path
```

## Retry Path (idempotent methods only: GET, PUT, DELETE)

```
On proxyRequest() failure (including 5xx via BackendError):
  ├─ check retry budget: skip if activeRetries/activeRequests > 20%
  ├─ debounce: skip DOWN mark if backend already marked unhealthy
  ├─ pool.MarkHealthy(failedURL, false)     — local update (only if still healthy)
  ├─ go updater.UpdateBackendStatus(DOWN)   — fire-and-forget Redis (debounced)
  ├─ pool.GetHealthy()                      — fresh snapshot (excludes failed)
  ├─ selectDifferent(healthy, exclude)      — pick min ActiveConnections
  ├─ atomic.Add(activeRetries, 1)
  ├─ resetBody(r)                           — replay buffered body
  └─ proxyRequest(w, r, retryURL)           — single retry attempt
```

Non-idempotent methods (POST, PATCH): no retry → 504 Gateway Timeout.

## Proxy Error Types

| Type | Trigger | Behavior |
|------|---------|----------|
| `TimeoutError` | `context.DeadlineExceeded` | Retry if idempotent |
| `BackendError` | 5xx HTTP response | Retry if idempotent |
| Client disconnect | `context.Canceled`, broken pipe, conn reset | No retry, no DOWN mark |

## Routing Algorithms

| Algorithm | File | Selection Logic | State |
|-----------|------|-----------------|-------|
| RoundRobin | `algorithms/RoundRobin.go:20` | `atomic.AddUint64(&next,1) % len(healthy)` | Stateless (atomic counter) |
| LeastConnections | `algorithms/LeastConnections.go:26` | Power of Two Choices: pick 2 random, select min connections | Lock-free atomic reads |
| Weighted | `algorithms/Weighted.go:44` | Random candidate, decrement weight, epoch reset | `sync.RWMutex` on weights map |

## Health Checking

- `health.Checker.Start()` — runs `checkAll()` on fixed interval
- `checkAll()` — one goroutine per backend (semaphore-bounded to 10), overlap guard via `atomic.Bool`
- `checkBackend()` — HTTP GET `{backend}/health`, 200 OK = healthy
- State transitions only → publish to Redis (avoids redundant writes)

## Metrics Collection

- Latency storage: reservoir sampling (max 10,000 samples) — bounded memory
- Percentiles computed on-demand via sorted copy (p50, p95, p99)
- Average latency tracked via running sum (exact, not sampled)
- Per-backend breakdown: request/success/failure/timeout counts + cumulative latency

## Metrics HTTP Server (port+1000)

```
GET  /metrics            → JSON summary (total, percentiles p50/p95/p99, per-backend)
GET  /metrics/timeseries → JSON array of 5s snapshots (RPS, avg latency, active backends)
GET  /metrics/export     → CSV download of time-series
GET  /health/backends    → JSON array of backend health statuses
```

On SIGTERM: drains HTTP connections (10s), then dumps `metrics.json` + `metrics.json.csv` to disk.

## Backend Server (cmd/backend)

```
GET  /health   → 200 OK (unconditional, used by health checker)
GET  /api/data → JSON response with X-Backend-ID header
```

Chaos injection headers (experiment use only):
- `X-Chaos-Error: <status>` — force HTTP error response
- `X-Chaos-Delay: <ms>` — artificial latency (may trigger proxy timeout)
- Baseline 5-25ms random delay simulates workload variance

## DNS Discovery + Connection Draining

`discovery.StartDNSWatcher()` polls Cloud Map DNS every 5s:
- Resolves hostname → IPv4 addresses
- Calls `pool.SyncServers(activeURLs, defaultWeight)`
- SyncServers preserves existing connections/health, adds new
- Backends removed from DNS with in-flight connections: marked `Draining=true`, `Healthy=false` (no new requests, completes existing)
- Backends removed from DNS with zero connections: dropped immediately

## Redis State Coordination

```
RedisManager.UpdateBackendStatus(url, status)
  ├─ SET "backend:{url}" → status   — persistent (for startup sync)
  └─ PUBLISH "lb-backend-events" → "url|status"  — real-time propagation

RedisManager.StartRedisWatcher()
  └─ SUBSCRIBE "lb-backend-events" → pool.MarkHealthy(url, status)

RedisManager.SyncOnStartUp()
  └─ GET "backend:{url}" for each server → pool.MarkHealthy()

RedisManager.StartPeriodicSync(30s)
  └─ Re-runs SyncOnStartUp to heal missed Pub/Sub messages
```

Auto-detects single-node vs cluster Redis based on comma-separated addresses.
