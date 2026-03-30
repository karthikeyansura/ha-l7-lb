# Milestone 1 Report -- HA-L7-LB

**CS6650: Building Scalable Distributed Systems**

**Team**: Sai Karthikeyan Sura, Zhaoshan "Joshua" Duan

**Date**: March 29, 2026

**Repo**: [github.com/karthikeyansura/ha-l7-lb](https://github.com/karthikeyansura/ha-l7-lb)

---

## 1. Problem, Team, and Overview of Experiments

### The Problem

Production load balancers like NGINX and HAProxy hide the trade-offs that matter most: how much latency does connection tracking add? Does retry logic actually mask failures? At what scale does cross-instance coordination bottleneck? This project builds a custom L7 load balancer from scratch in Go to make these trade-offs measurable through controlled experiments on AWS ECS Fargate.

### The Team

**S Karthikeyan S.** -- Primary author: Built the LB (Issues and PRs #1-65), all Terraform modules, and led the AWS deployment.

**Zhaoshan "Joshua" Duan** -- Contributor: Conducted code reviews for all PRs, fixed early-stage bugs, improved documentation, performed heterogeneous backend experiments, and investigated ECS API discovery.

### Overview of Experiments

| Experiment | Question | Key Metrics |
|-----------|----------|-------------|
| **1. Routing Overhead** | Does least-connections improve tail latency vs. round-robin? | p50/p95/p99 latency, RPS, per-backend distribution |
| **2. Retry Efficacy** | Does idempotent retry reduce client-visible errors under failure? | Error rate, retry rate, latency impact |
| **3. Horizontal Scaling** | Does adding LB instances scale linearly, or does Redis bottleneck? | Aggregate RPS, scaling efficiency |

**Role of AI**: Claude Code was used for code review and documentation (~30% time savings). AI was **not** used to write the core Go implementation.

**Observability**: The LB exposes `/metrics`, `/metrics/timeseries`, `/metrics/export` (CSV), and `/health/backends` on port+1000. Metrics dump to disk on SIGTERM. CloudWatch Logs capture all ECS output.

---

## 2. Project Plan and Recent Progress

**Done**: Core implementation (Mar 16), hardening Issues and PRs #25-65 (Mar 16-28), AWS deployment (Mar 28-29), Exp 1 runs in progress (Mar 29). **Planned**: Exp 2 (Mar 31-Apr 5), Exp 3 (Apr 6-12), final report (Apr 13-19).

**S Karthikeyan S.**: All LB code and PRs, Terraform, AWS deployment, Exp 1 on homogeneous backends, Exp 3. 

**Joshua**: Early fixes, PR review, ECS API discovery investigation (abandoned -- Learner Lab blocks Cloud Map, pivoted to personal AWS), heterogeneous backend deployment, Exp 1 RR+LC on strong/weak backends, Exp 2, documentation and report.

---

## 3. Objectives

**Short-term**: Execute all three experiments on AWS with statistically meaningful data and deliver a final report with charts and analysis.

**Long-term**: Open-source the LB as a reference implementation, add Prometheus/Grafana observability (replacing CSV exports), and expand experiments to cover IP affinity, circuit breakers, and multi-region Redis coordination. If AI-based routing were added, we would enforce deterministic fallback on model failure and A/B testing before rollout.

---

## 4. Related Work

Our Redis Pub/Sub model uses eventual consistency (commutative health transitions, no Lamport ordering). Experiment 3 tests the course hypothesis that shared state bottlenecks as instances scale. Our least-connections uses Mitzenmacher's "Power of Two Choices" (2001) for near-optimal O(1) routing. Compared to NGINX/HAProxy/Envoy, our system is simpler (Redis Pub/Sub + Cloud Map DNS) but less feature-rich.

### Related Class Projects

1. **Raft KV Store with Chaos Engineering** (Qian Li et al.):
     - Both: distributed Go + fault injection. 
     - Diff: theirs is CP (consensus); ours is AP (serve with stale state).
2. **Onion Routing on ECS Fargate** (Arjun Avadhani, Rahul Suresh):
     - Both: custom Go infra on Fargate + scaling + failure tests.
     - Diff: their relays are stateless post-circuit; ours coordinate via Redis.
3. **LLM Inference Routing on K8s** (Akshay Keerthi, Ajin Frank Justin)
     - Both: custom Go router with multiple LB algorithms + p50/p95/p99 comparison.
     - Diff: their backends are GPU-bound with variable cost; ours are uniform web services.

---

## 5. Methodology

`Client → NLB (L4) → LB ECS Tasks (L7) → Backend ECS Tasks`, with ElastiCache Redis for Pub/Sub state sync and Cloud Map DNS for discovery (5s poll). The proxy retries idempotent methods (GET/PUT/DELETE) with a 20% retry budget cap. Redis is optional -- the LB degrades to local-only health state if unavailable. Experiments run on both homogeneous (S Karthikeyan S.) and heterogeneous backends (Joshua: 1 strong 512 CPU + 1 weak 256 CPU).

**Exp 1 -- Routing Algorithm Comparison**: RR vs. LC vs. weighted on heterogeneous backends. Measures p50/p95/p99 latency, RPS, and per-backend distribution. Trade-off: stateless simplicity vs. connection-aware fairness.

**Exp 2 -- Failure Isolation**: Chaos injection (20% forced 500s, 10% artificial delays) with retry enabled vs. disabled. Measures client error rate and retry overhead. Trade-off: reliability improvement vs. added latency and backend load.

**Exp 3 -- Horizontal Scaling**: 1/2/4/8 LB instances sharing one Redis under fixed load. Measures scaling efficiency. Trade-off: throughput capacity vs. Redis Pub/Sub fan-out overhead.

**Observability**: Each run produces Locust CSV (client-side), LB metrics JSON+CSV (server-side, dumped on SIGTERM), and CloudWatch Logs. AI (Claude Code) reviews code for concurrency correctness and generates documentation but does not make routing decisions or execute experiments.

---

## 6. Preliminary Results

### Exp 1 — Homogeneous Backends (S Karthikeyan S.)

Identical backends, 2 LB instances, 500 users, 5 min per algorithm.

| Metric | Round-Robin | Least-Connections | Weighted | Best |
|--------|-------------|-------------------|----------|------|
| RPS | 1,269 | 1,482 | 1,523 | Weighted (+20%) |
| p50 | 230ms | 200ms | 200ms | LC/W (-13%) |
| p95 | 500ms | 340ms | 330ms | Weighted (-34%) |
| p99 | 920ms | 440ms | 410ms | Weighted (-55%) |
| Max | 2,698ms | 1,314ms | 663ms | Weighted (-75%) |

With identical backends, LC and weighted dramatically outperform RR — p99 drops by 52-55% and max latency by 51-75%. Weighted edges out LC slightly, likely due to more predictable distribution.

### Exp 1 — Heterogeneous Backends (Joshua)

1 strong (512 CPU) + 1 weak (256 CPU), 2 LB instances, 500 users, 5 min per algorithm.

| Metric | Round-Robin | Least-Connections | Delta |
|--------|-------------|-------------------|-------|
| RPS | 2,114 | 2,135 | +1.0% |
| p95 | 160ms | 150ms | -6.3% |
| p99 | 280ms | 270ms | -3.6% |
| Max | 741ms | 580ms | -21.7% |

LC provides modest tail latency improvement but the LB was the bottleneck at ~99% CPU, limiting observable differences. Weak backend ran ~71% CPU vs. strong at ~37% under both algorithms. Weighted is deferred -- single DNS query assigns equal weights to all backends. Fix in progress: multiple DNS watchers with per-service weights (Issue #64).

### Remaining & Expected Outcomes

Exp 1 RR+LC done; weighted blocked. **Exp 2** (chaos/retry) and **Exp 3** (1/2/4/8 LB scaling) planned. We expect retry to reduce client errors by >50% under failure injection, and near-linear scaling up to 4 LBs with sublinear returns at 8.

**Worst case**: All backends fail simultaneously -- every request attempts, fails, retries, fails again (504). The 20% retry budget prevents cascading amplification. 

**Base case**: All healthy, LB adds <1ms overhead.

---

## 7. Impact

Most teams adopt "least-connections" or "round-robin" based on convention, not data. Our results quantify when stateful routing justifies its overhead, whether retry actually helps clients, and where coordinated LBs stop scaling. The codebase uses pluggable interfaces -- other students can add algorithms, swap coordination mechanisms, or point the LB at their own backends for joint testing.
