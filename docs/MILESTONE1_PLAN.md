# Milestone 1 Plan — HA-L7-LB

**Due**: Monday March 30, 2026 9am
**Team**: Sai Karthikeyan Sura, Zhaoshan "Joshua" Duan

## Deliverables

1. **Repo link** to code and tests (done — GitHub repo exists)
2. **Detailed project plan** with task assignments
3. **Initial charts and graphs** from early experimental results
4. **2-minute video walkthrough** (elevator pitch)
5. **Short report** (max 5 pages)

## Phase Breakdown

### Phase 1: Codebase Visualizations

Create diagrams demonstrating architecture understanding:

- Architecture diagram — Client -> NLB -> LB Tasks -> Backends, with Redis and Cloud Map
- Call flow diagram — Request lifecycle through proxy (happy path + retry)
- Health state propagation diagram — Cross-instance health sync via Redis Pub/Sub
- Component dependency map — Package imports and call relationships
- Algorithm comparison visual — RoundRobin vs LeastConnections vs Weighted

Format: Mermaid diagrams (GitHub-renderable) in `docs/diagrams/`

### Phase 2: Initial Local Experiments

Run small-scale tests locally to produce initial charts:

1. Experiment 1 preview: LB with round-robin vs least-connections, compare RPS/latency
2. Experiment 2 preview: Inject chaos headers, measure retry success rate
3. Export CSV via metrics endpoint, generate charts with Python/matplotlib

Output: `docs/charts/` with PNG graphs and raw CSV data

### Phase 3: Project Plan Document

Structured task breakdown:
- Remaining work: full AWS deployment, 3 experiments at scale, final report
- Timeline with milestones
- Team member assignments

Output: `docs/PROJECT_PLAN.md` or section in report

### Phase 4: Report (5 pages max)

Sections:
1. Introduction & Motivation (~0.5 page)
2. Architecture Overview (~1 page) — diagrams from Phase 1
3. Current Implementation Status (~1 page) — what's built, test coverage, key decisions
4. Initial Results (~1 page) — charts from Phase 2
5. Project Plan & Timeline (~1 page)
6. Team Contributions (~0.5 page)

Output: `docs/MILESTONE1_REPORT.md`

### Phase 5: Video (2 minutes)

- Architecture diagram walkthrough (30s)
- Live demo: start LB + backends, show request routing (45s)
- Show metrics endpoint with live data (30s)
- Project plan and next steps (15s)

## Team Contributions

### Sai Karthikeyan Sura (Primary Author)
- Designed and implemented the entire LB architecture
- Built all core subsystems: proxy, algorithms, health checker, metrics, Redis sync, DNS discovery
- Created Terraform infrastructure modules
- Wrote Locust experiment definitions
- Authored backend chaos injection server

### Joshua (Contributor)
- PRs and bug fixes
- Redis error handling improvements (distinguish redis.Nil from network errors)
- Test thread safety and assertion fixes
- Weighted algorithm zero-weight guard
- DNS watcher ticker leak fix

## Codebase Summary (for reference)

### Architecture
```
Client -> NLB (L4) -> LB ECS tasks (L7) -> Backend ECS tasks
                           |
                     ElastiCache Redis (Pub/Sub state sync)
                           |
                     Cloud Map DNS (service discovery)
```

### Key Interfaces
- `repository.SharedState` — Backend pool operations
- `algorithms.Rule` — `GetTarget(*SharedState, *http.Request) (url.URL, error)`
- `health.StatusUpdater` — Propagates health changes to Redis
- `metrics.Collector` — Request-level recording and time-series snapshots

### Algorithms
- RoundRobin: atomic counter, stateless
- LeastConnections: scan for min active connections, random tie-break
- Weighted: proportional distribution with epoch-based reset

### Concurrency Model
- `ServerState.Healthy`: atomic.Bool (lock-free reads)
- `ServerState.ActiveConnections`: atomic.Int64
- `InMemory.mu`: sync.RWMutex for servers slice
- `metrics.Collector.mu`: sync.RWMutex for metrics data

### Proxy Retry Logic
- Buffers request body upfront for replay
- Retries only idempotent methods (GET, PUT, DELETE)
- Marks failed backend DOWN locally, propagates to Redis async
- Selects retry target with fewest active connections from fresh pool

### Test Coverage
- proxy_test.go: forwarding, retry, connection tracking, body preservation
- algorithms_test.go: all 3 algorithms, concurrency safety
- in_memory_test.go: CRUD, sync, concurrent access
- checker_test.go: health transitions, nil updater
- collector_test.go: counting, percentiles, CSV export, concurrency

### Experiments (Locust)
1. Stateless vs stateful routing overhead (round-robin vs least-connections)
2. Failure isolation and retry efficacy under chaos injection
3. Horizontal LB scaling vs Redis state contention (1/2/4/8 LB instances)
