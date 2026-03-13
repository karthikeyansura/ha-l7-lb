# HA-L7-LB

High-Availability Layer 7 Load Balancer with Distributed State Coordination.

Custom L7 reverse proxy in Go with pluggable routing algorithms, active health checking, idempotent-method retry logic, and Redis Pub/Sub state coordination across horizontally scaled LB instances. Deployed on AWS ECS Fargate via Terraform.

## Architecture

```
Client -> NLB (L4) -> LB ECS tasks (L7) -> Backend ECS tasks
                           |
                     ElastiCache Redis
```

## Algorithms

- Round Robin: stateless sequential distribution.
- Least Connections: stateful, routes to the backend with fewest in-flight requests.
- Weighted: configurable traffic proportions per backend.

## Experiments

1. Stateless vs. Stateful Routing Overhead (round-robin vs. least-connections).
2. Failure Isolation and Retry Efficacy under Chaos (X-Chaos-Error, X-Chaos-Delay headers).
3. Horizontal LB Scaling vs. Redis State Contention (1, 2, 4, 8 LB instances behind NLB).

## Setup

```bash
go mod tidy
go build ./cmd/lb
go build ./cmd/backend
docker build -f Dockerfile.lb -t ha-l7-lb .
docker build -f Dockerfile.backend -t ha-l7-backend .
cd terraform && terraform init && terraform apply
```
