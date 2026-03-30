# CloudWatch Metrics - Round-Robin (500 users, 5 min)
# Time window: 2026-03-29 6:05-6:10 PM PDT (2026-03-30 01:05-01:10 UTC)

## CPU Utilization (during steady-state load)

| Service | CPU avg | CPU max |
|---------|---------|---------|
| Backend Strong (512 CPU / 1024 MB) | ~37% | ~38% |
| Backend Weak (256 CPU / 512 MB) | ~71% | ~72% |
| LB (2 instances) | ~99% | ~100% |

## Memory Utilization (during steady-state load)

| Service | Mem avg | Mem max |
|---------|---------|---------|
| Backend Strong | ~0.68% | ~0.78% |
| Backend Weak | ~1.43% | ~1.56% |
| LB | ~3.0% | ~4.0% |