<!-- Generated: 2026-03-29 | Files scanned: 22 | Token estimate: ~300 | Last commit: 64e995f -->
# Dependencies

## Go Module

`github.com/karthikeyansura/ha-l7-lb` — Go 1.25

## External Libraries

| Library | Purpose | Used In |
|---------|---------|---------|
| `github.com/redis/go-redis/v9` | Redis client (single-node + cluster) | `internal/repository/redismanager/redis.go` |
| `gopkg.in/yaml.v3` | YAML config parsing | `internal/config/config.go` |

## Infrastructure (AWS)

| Service | Role |
|---------|------|
| NLB (Network Load Balancer) | L4 front-door for LB tasks |
| ECS Fargate (LB tasks) | Runs ha-l7-lb binary |
| ECS Fargate (Backend tasks) | Runs backend binary |
| ElastiCache Redis | Pub/Sub health state sync across LB instances |
| Cloud Map | Internal DNS for backend service discovery |
| ECR | Docker image registry |
| CloudWatch Logs | Container logging |

## Terraform Modules

```
terraform/modules/
  network/       — VPC, subnets, security groups
  ecr/           — Container registries (lb + backend)
  ecs-lb/        — LB Fargate service + task definition
  ecs-backend/   — Backend Fargate service + task definition
  nlb/           — Network Load Balancer + target groups
  elasticache/   — Redis cluster/single-node
  autoscaling/   — ECS auto scaling policies
  logging/       — CloudWatch log groups
```

## Docker Images

| Dockerfile | Image | Base |
|------------|-------|------|
| `Dockerfile.lb` | ha-l7-lb | golang:1.25-alpine → alpine |
| `Dockerfile.backend` | ha-l7-backend | golang:1.25-alpine → alpine |

## Load Testing

- Locust (Python) in `locust/locustfile.py`
- Three experiment profiles: routing comparison, chaos injection, horizontal scaling
- Docker Compose for Locust master/worker orchestration
