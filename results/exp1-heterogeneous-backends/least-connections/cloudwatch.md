# CloudWatch Metrics - Least-Connections (500 users, 5 min)
# Time window: 2026-03-29 6:24-6:29 PM PDT (2026-03-30 01:24-01:29 UTC)

## CPU Utilization (during steady-state load)

| Service | CPU avg | CPU max |
|---------|---------|---------|
| Backend Strong (512 CPU / 1024 MB) | ~36% | ~37% |
| Backend Weak (256 CPU / 512 MB) | ~72% | ~73% |
| LB (2 instances) | ~99% | ~100% |

## Memory Utilization (during steady-state load)

| Service | Mem avg | Mem max |
|---------|---------|---------|
| Backend Strong | ~0.68% | ~0.68% |
| Backend Weak | ~1.37% | ~1.46% |
| LB | ~3.1% | ~3.2% |
