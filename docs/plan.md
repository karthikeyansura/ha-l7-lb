# Project Plan -- HA-L7-LB

**CS6650: Building Scalable Distributed Systems**

**Team**: Sai Karthikeyan Sura, Zhaoshan "Joshua" Duan

**Date**: March 29, 2026

**Repo**: [github.com/karthikeyansura/ha-l7-lb](https://github.com/karthikeyansura/ha-l7-lb)

---

## 1. Project Overview

This project implements a high-availability Layer 7 load balancer in Go with distributed state coordination. The system features three pluggable routing algorithms (round-robin, least-connections, weighted), active health checking with Redis Pub/Sub cross-instance synchronization, DNS-based service discovery via AWS Cloud Map, and idempotent-method retry logic. The load balancer is deployed on AWS ECS Fargate behind a Network Load Balancer, with ElastiCache Redis for state synchronization and Cloud Map for backend discovery.

The project includes a Locust-based load testing framework with three experiments that evaluate routing algorithm overhead, failure isolation under chaos injection, and horizontal scaling behavior against Redis state contention.

---

## 2. Current Status

### Completed

| Component | Status | Notes                                         |
|-----------|--------|-----------------------------------------------|
| Core reverse proxy with request buffering and replay | Done | `internal/proxy/`                             |
| Round-robin algorithm (atomic counter) | Done | `internal/algorithms/`                        |
| Least-connections algorithm (min active connections) | Done | `internal/algorithms/`                        |
| Weighted algorithm (proportional distribution) | Done | `internal/algorithms/`                        |
| Active health checker with configurable interval | Done | `internal/health/`                            |
| Redis Pub/Sub state synchronization | Done | `internal/repository/redismanager/`           |
| Periodic Redis state sync (heal missed Pub/Sub events) | Done | PR #30                                        |
| DNS-based service discovery (Cloud Map polling) | Done | `internal/discovery/`                         |
| Metrics collector (latency, RPS, per-backend stats) | Done | `internal/metrics/`                           |
| Graceful Redis degradation (local-only mode) | Done | Redis is optional at startup                  |
| Backend server with chaos injection headers | Done | `cmd/backend/`                                |
| YAML configuration with env variable overrides | Done | `config.yaml`                                 |
| Dockerfiles for LB and backend | Done | `Dockerfile.lb`, `Dockerfile.backend`         |
| Terraform modules (network, ECR, ECS-LB, ECS-backend, NLB, ElastiCache, autoscaling, logging) | Done | `terraform/modules/`                          |
| Locust experiment definitions (3 experiments) | Done | `locust/locustfile.py`                        |
| Unit tests (algorithms, health, metrics, proxy, repository) | Done | All passing                                   |
| Bug fixes: test panics (PR #25), DNS ticker leak (PR #26), weighted zero-weight guard (PR #27), Redis docs (PR #28), Redis error handling (PR #29), periodic sync (PR #30), HTTP client reuse (PR #31), health checker concurrency (PR #32), body close response (PR #33) | Done | PRs #25-33                                    |
| Max body size limit (10MB OOM protection) | Done | PR #34                                        |
| Configurable proxy timeout (was hardcoded 2s) | Done | PR #35                                        |
| 5xx responses trigger retry logic | Done | PR #36                                        |
| Client disconnect detection (skip DOWN marking) | Done | PR #37                                        |
| Debounced Redis DOWN writes | Done | PR #38                                        |
| Retry budget (20% cap, prevents cascading failures) | Done | PR #39                                        |
| Connection draining on DNS removal | Done | PR #40                                        |
| Graceful shutdown with HTTP connection draining | Done | PR #41                                        |
| Bounded latency storage with reservoir sampling | Done | PR #42                                        |
| Power of Two Choices for least-connections algorithm | Done | PR #43                                        |
| Post-hardening polish (test fixes, draining integration, docs) | Done | Issues #46-53, PRs #54-61                     |
| AWS deployment (ECS Fargate, NLB, ElastiCache, Cloud Map) | Done | S Karthikeyan S. deployed full stack          |
| Heterogeneous backend topology (strong 512 CPU + weak 256 CPU) | Done | Joshua deployed on personal AWS               |
| ECS API discovery investigation (alternative to Cloud Map) | Done | Joshua — abandoned, not worth complexity      |
| Experiment 1: RR + LC on homogeneous backends | Done | S Karthikeyan S. — results in `results/`      |
| Experiment 1: RR + LC on heterogeneous backends | Done | Joshua — results in `results/`                |
| Experiment 1: Weighted on homogeneous backends | Done | S Karthikeyan S. — results in `results/`      |
| Goroutine leak and response corruption fix | In Progress | Joshua — PR #62 (open, not yet merged)        |
| Graceful shutdown race: metrics flush before exit | In Progress | Issue #63 — blocked on PR #62 merge           |
| Milestone 1 report and documentation overhaul | Done | Joshua — in `docs/milestone_1/report.md` |

### Test Coverage

All test packages pass. Packages with test files: `algorithms`, `health`, `metrics`, `proxy`, `repository`. Packages without test files: `cmd/backend`, `cmd/lb`, `config`, `discovery`, `repository/redismanager`.

---

## 3. Development

### Phase 1: AWS Deployment and Validation (Week 1)

Deployed the full stack to AWS and verified end-to-end connectivity.

| Task | Description |
|------|-------------|
| 1.1 Push Docker images to ECR | Build `linux/amd64` images for LB and backend, push to ECR repositories |
| 1.2 Apply Terraform | Run `terraform init && terraform apply` to provision VPC, NLB, ECS services, ElastiCache, Cloud Map |
| 1.3 Verify Cloud Map DNS resolution | Confirm LB tasks can resolve `api.internal` to backend task IPs; check ECS logs for DNS watcher output |
| 1.4 Verify ElastiCache connectivity | Confirm LB tasks connect to Redis; check logs for "connected to redis" vs degraded-mode warning |
| 1.5 Smoke test through NLB | Send manual requests through the NLB DNS name; confirm proxying to backends and health check responses |
| 1.6 Validate health checker behavior | Kill a backend task, verify LB detects it as unhealthy within the check interval, confirm Redis propagation to other LB instances |
| 1.7 Validate retry behavior | Send chaos headers through NLB, confirm GET retries succeed on alternate backend and POST failures are not retried |

### Phase 2: Experiment Execution (Weeks 2-3)

Run all three experiments with varying parameters. Each experiment produces CSV data from Locust.

**Experiment 1: Stateless vs. Stateful Routing Overhead**

| Task | Description                                                                                                                                                    |
|------|----------------------------------------------------------------------------------------------------------------------------------------------------------------|
| 2.1 Run round-robin baseline | **Done -** S Karthikeyan S. (homogeneous): 1,269 RPS, p99 920ms. Joshua (heterogeneous): 2,114 RPS, p99 280ms, weak ~71% CPU, LB ~99% CPU.                     |
| 2.2 Run least-connections comparison | **Done -** S Karthikeyan S. (homogeneous): 1,482 RPS, p99 440ms. Joshua (heterogeneous): 2,135 RPS, p99 270ms.                                                  |
| 2.3 Run weighted comparison | **Done (homogeneous only) -** S Karthikeyan S.: 1,523 RPS, p99 410ms. Heterogeneous blocked — single DNS query assigns equal weights. Fix in progress (Issue #64). |
| 2.4 Collect metrics snapshots | **Done -** CloudWatch CPU/memory collected for all runs. Results in `results/`.                                                                                  |

**Experiment 2: Failure Isolation and Retry Efficacy**

| Task | Description |
|------|-------------|
| 2.5 Run with retries enabled | Deploy default config, run `ChaosInjectionUser` at 50, 100, 200 users for 5 min each |
| 2.6 Run with retries disabled | Modify `isIdempotent` to return false, rebuild and redeploy, repeat identical runs |
| 2.7 Capture error rate differential | Compare client-observed 5xx rates and p95/p99 latency between retry-enabled and retry-disabled runs |

**Experiment 3: Horizontal Scaling vs. Redis State Contention**

| Task | Description |
|------|-------------|
| 2.8 Single LB instance baseline | Set `lb_count = 1` in Terraform, run `ScalingBaselineUser` at 500, 1000, 2000 users for 5 min |
| 2.9 Scale to 2 LB instances | Set `lb_count = 2`, re-apply Terraform, repeat same Locust runs |
| 2.10 Scale to 4 LB instances | Set `lb_count = 4`, re-apply, repeat |
| 2.11 Scale to 8 LB instances | Set `lb_count = 8`, re-apply, repeat |
| 2.12 Spike load test | Run `ScalingSpikeUser` at each LB count (1/2/4/8) for 3 min burst |

### Phase 3: Data Analysis and Visualization (Week 3-4)

| Task | Description |
|------|-------------|
| 3.1 Process Locust CSV exports | Parse files from each run |
| 3.2 Experiment 1 charts | Bar charts: RPS by algorithm at each user count. Line charts: p50/p95/p99 latency by algorithm |
| 3.3 Experiment 2 charts | Side-by-side error rate comparison (retry-enabled vs disabled). Latency CDF plots |
| 3.4 Experiment 3 charts | Scaling efficiency chart: RPS vs. LB instance count (ideal linear vs. observed). Latency heatmap by instance count |
| 3.5 Per-backend analysis | Use metrics endpoint data to show load distribution fairness across backends |
| 3.6 Statistical summary | Compute mean, median, std dev, and confidence intervals for key metrics |

### Phase 4: Final Report and Presentation (Week 3-4)

| Task | Description |
|------|-------------|
| 4.1 Write introduction and architecture section | System design, component interactions, data flow diagrams |
| 4.2 Write implementation section | Key design decisions: concurrency model, retry strategy, degraded mode, algorithm trade-offs |
| 4.3 Write experiment methodology | Describe each experiment setup, parameters, and measurement approach |
| 4.4 Write results and analysis | Present charts with interpretation; discuss whether results match expectations |
| 4.5 Write conclusions | Lessons learned, limitations, future work |
| 4.6 Prepare presentation slides | 10-15 slides covering architecture, experiments, results, and demo |
| 4.7 Rehearse demo | Live walkthrough: deploy, run Locust, show metrics, trigger chaos, demonstrate retry |
| 4.8 Terraform teardown | Destroy all AWS resources after final submission |

---

## 4. Task Assignment

| Task ID | Task | Owner            | Status                                                      |
|---------|------|------------------|-------------------------------------------------------------|
| 1.1 | Push Docker images to ECR | S Karthikeyan S. | Done                                                        |
| 1.2 | Apply Terraform and provision infrastructure | S Karthikeyan S. | Done                                                        |
| 1.3 | Verify Cloud Map DNS resolution | S Karthikeyan S. | Done                                                        |
| 1.4 | Verify ElastiCache connectivity | S Karthikeyan S. | Done                                                        |
| 1.5 | Smoke test through NLB | S Karthikeyan S. | Done                                                        |
| 1.6 | Validate health checker behavior | S Karthikeyan S. | Done                                                        |
| 1.7 | Validate retry behavior with chaos headers | S Karthikeyan S. | Done                                                        |
| 2.1 | Run round-robin baseline (Exp 1) | Both             | Done (S Karthikeyan S.: homogeneous, Joshua: heterogeneous) |
| 2.2 | Run least-connections comparison (Exp 1) | Both             | Done (S Karthikeyan S.: homogeneous, Joshua: heterogeneous) |
| 2.3 | Run weighted comparison (Exp 1) | S Karthikeyan S. | Done (homogeneous only; heterogeneous blocked)              |
| 2.4 | Collect metrics snapshots (Exp 1) | Both             | Done                                                        |
| 2.5 | Run retries-enabled chaos test (Exp 2) | Joshua           | Planned                                                     |
| 2.6 | Run retries-disabled chaos test (Exp 2) | Joshua           | Planned                                                     |
| 2.7 | Capture error rate differential (Exp 2) | Joshua           | Planned                                                     |
| 2.8 | Single LB instance baseline (Exp 3) | S Karthikeyan S. | Planned                                                     |
| 2.9 | Scale to 2 LB instances (Exp 3) | S Karthikeyan S. | Planned                                                     |
| 2.10 | Scale to 4 LB instances (Exp 3) | S Karthikeyan S. | Planned                                                     |
| 2.11 | Scale to 8 LB instances (Exp 3) | S Karthikeyan S. | Planned                                                     |
| 2.12 | Spike load test at each scale (Exp 3) | S Karthikeyan S. | Planned                                                     |
| 3.1 | Process Locust CSV exports | Joshua           | Planned                                                     |
| 3.2 | Experiment 1 charts (algorithm comparison) | Both             | Planned                                                     |
| 3.3 | Experiment 2 charts (chaos/retry comparison) | Joshua           | Planned                                                     |
| 3.4 | Experiment 3 charts (scaling efficiency) | Joshua           | Planned                                                     |
| 3.5 | Per-backend load distribution analysis | Joshua           | Planned                                                     |
| 3.6 | Statistical summary and confidence intervals | Joshua           | Planned                                                     |
| 4.1 | Write introduction and architecture section | Joshua           | Planned                                                     |
| 4.2 | Write implementation section | S Karthikeyan S. | Planned                                                     |
| 4.3 | Write experiment methodology section | Joshua           | Done                                                        |
| 4.4 | Write results and analysis section | Joshua           | Done (Exp 1)                                                |
| 4.5 | Write conclusions section | Joshua           | Planned                                                     |
| 4.6 | Prepare presentation slides | Joshua           | Planned                                                     |
| 4.7 | Rehearse demo | Both             | Planned                                                     |

---

## 5. Timeline

| Dates | Milestone | Deliverables |
|-------|-----------|-------------|
| Mar 28 -- Mar 29 | AWS Deployment and Validation | Infrastructure provisioned, smoke tests passing, health checker and retry behavior verified end-to-end |
| Mar 31 -- Apr 5 | Experiment 1 and 2 Execution | All algorithm comparison runs complete (round-robin, least-connections, weighted at 4 user levels). All chaos injection runs complete (retries enabled and disabled at 3 user levels). CSV data collected. |
| Apr 6 -- Apr 12 | Experiment 3 Execution and Data Analysis | All horizontal scaling runs complete (1/2/4/8 LB instances, baseline + spike). All charts finalized. |
| Apr 13 -- Apr 19 | Final Report, Presentation, Submission | Report polished and submitted. Presentation slides complete. Demo rehearsed. AWS resources destroyed. |

---

## 6. Risk Register

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|-----------|
| AWS cost overrun from long-running ECS tasks | Medium | Medium | Set billing alerts at $50 and $100. Tear down infrastructure immediately after each experiment session. Use `terraform destroy` between sessions if gap is more than a day. |
| ElastiCache connectivity failure in ECS | Medium | High | Verify security group rules allow traffic on port 6379 between LB tasks and ElastiCache subnet. LB runs in degraded mode without Redis, so experiments can proceed with local-only health state if needed. |
| Cloud Map DNS resolution not working | Low | High | Verify Cloud Map namespace and service are created by Terraform. Check ECS task networking (awsvpc mode) places tasks in the correct VPC. Fall back to hardcoded backend IPs in config.yaml as a temporary workaround. |
| Locust cannot generate enough load for Experiment 3 | Medium | Medium | Scale Locust workers via `docker-compose up --scale worker=4`. If still insufficient, run Locust on an EC2 instance in the same region to eliminate network latency overhead. |
| Non-reproducible experiment results | Medium | Medium | Run each configuration at least 3 times. Use 5-minute steady-state windows (discard ramp-up period). Report standard deviation alongside mean values. |
| Terraform state conflicts between team members | Low | Medium | Only one person applies Terraform at a time. Use remote state backend (S3) if conflicts occur. Communicate before running `terraform apply`. |
| Redis Pub/Sub message loss under extreme load (Exp 3) | Medium | Low | The periodic sync (PR #30) already mitigates this by healing missed events every 30 seconds. Monitor Redis memory and connection count during experiments. |
| ECS task placement failures at 8 LB instances | Low | Medium | Ensure the VPC has sufficient IP addresses across availability zones. Use Fargate Spot for cost savings but fall back to on-demand if spot capacity is insufficient. |
| Backend server performance ceiling masks LB differences | Low | Medium | The backend `/api/data` handler is lightweight (JSON echo). If backend becomes the bottleneck, scale backend task count independently via Terraform `backend_count` variable. |
