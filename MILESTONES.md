# Taskforge Milestones

Taskforge is a cloud-native task execution platform written in Go.

This document tracks the roadmap from an in-process prototype to a distributed,
cloud-native task platform.

## Phase 1: Core Runtime (Milestones 1-4)

Goal: Build the basic task execution runtime.

### Milestone 1: Project Structure ✅

Create a clean Go project layout.

Features:

- Go module
- repo structure

Example layout:

```text
cmd/
internal/
pkg/
configs/
```

Status: `DONE`

### Milestone 2: CLI Interface ✅

Provide CLI commands to interact with the system.

Commands:

- `taskforge worker`
- `taskforge enqueue`
- `taskforge result`
- `taskforge demo`

Status: `DONE`

### Milestone 3: Task Model ✅

Implement the core task abstraction.

Features:

- task registry
- payload handling
- task metadata
- result model

Example:

```go
type Task struct {
    ID         string
    Payload    any
    Status     string
    RetryCount int
    Timeout    time.Duration
    Queue      string
    Priority   int
}
```

Status: `DONE`

### Milestone 4: Worker Runtime ✅

Implement the task execution engine.

Features:

- worker pool
- goroutine concurrency
- panic recovery
- task timeout
- retry execution

Status: `DONE`

## Phase 2: Reliability (Milestones 5-8)

Goal: Make task execution reliable and fault-tolerant.

### Milestone 5: Retry Strategy

Improve retry logic.

Features:

- exponential backoff
- retry delay scheduling
- configurable retry policies

Example formula:

```text
retry_delay = base * 2^attempt
```

Status: `TODO`

### Milestone 6: Dead Letter Queue

Handle permanently failed tasks.

Features:

- max retry limit
- DLQ storage
- DLQ inspection

Status: `TODO`

### Milestone 7: Persistence Layer ⭐

Move from in-memory to persistent broker.

Supported backends:

- Redis
- PostgreSQL
- NATS (optional)

Architecture:

```text
API -> Broker -> Worker -> Result Store
```

Status: `TODO (HIGH PRIORITY)`

### Milestone 8: Idempotency

Prevent duplicate task execution.

Features:

- deduplication keys
- idempotency enforcement
- task locking

Status: `TODO`

## Phase 3: Observability (Milestones 9-12)

Goal: Provide production-grade monitoring.

### Milestone 9: Structured Logging

Features:

- JSON logs
- request context logging
- task execution logs

Libraries:

- `slog`
- `zap`

Status: `TODO`

### Milestone 10: Metrics

Integrate monitoring.

Metrics:

- queue size
- job latency
- worker utilization
- task failures

Stack:

- Prometheus
- Grafana

Status: `TODO`

### Milestone 11: Distributed Tracing

Trace task execution across services.

Stack:

- OpenTelemetry

Flow:

```text
API -> Queue -> Worker -> Result
```

Status: `TODO`

### Milestone 12: Health Checks

Expose service health endpoints.

Endpoints:

- `/health`
- `/ready`

Used by:

- Kubernetes probes

Status: `TODO`

## Phase 4: Cloud Native Deployment (Milestones 13-16)

Goal: Deploy Taskforge as a cloud-native system.

### Milestone 13: Containerization

Create container images.

Artifacts:

- `Dockerfile`
- multi-stage build

Status: `TODO`

### Milestone 14: Local Dev Environment

Run the stack locally.

Tools:

- `docker-compose`

Services:

- api
- worker
- redis
- prometheus

Status: `TODO`

### Milestone 15: Kubernetes Deployment

Deploy services to Kubernetes.

Resources:

- Deployment
- Service
- ConfigMap
- Secret

Status: `TODO`

### Milestone 16: Autoscaling

Scale workers automatically.

Methods:

- HPA
- queue length metrics

Status: `TODO`

## Phase 5: Advanced Features (Milestones 17-20)

Goal: Turn Taskforge into a workflow platform.

### Milestone 17: Scheduled Tasks

Features:

- delayed tasks
- cron scheduling

Status: `TODO`

### Milestone 18: Rate Limiting

Control task throughput.

Features:

- per-queue rate limit
- per-tenant limit

Status: `TODO`

### Milestone 19: DAG Workflows

Support task dependencies.

Example:

```text
Task B depends on Task A
```

Similar to:

- Airflow
- Temporal

Status: `TODO`

### Milestone 20: Web Dashboard

Provide UI for monitoring.

Features:

- task status
- worker stats
- metrics view

Status: `TODO`
