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
- DLQ replay

Recommended order of action:

1. persist terminally failed task envelopes in a Redis DLQ keyspace
2. include final failure metadata needed for inspection and replay decisions
3. add library APIs to list and fetch DLQ entries across app instances
4. add CLI inspection support for Redis-backed DLQ state
5. add tests covering retry exhaustion, DLQ persistence, and cross-process reads

Why this comes next:

- persistence is now in place, so failure state can be shared across processes
- retries already exist, so DLQ closes the reliability loop with immediate
  operator value
- it creates a clean foundation for later idempotency and observability work

Delivered:

- dedicated DLQ storage boundary with memory and Redis backends
- terminal-failure persistence from the worker on retry exhaustion
- library APIs to fetch, list, and replay DLQ entries
- CLI support for `dlq list`, `dlq get`, and `dlq replay`
- unit and Redis integration coverage for DLQ persistence, inspection, and replay

Proposed scope split:

1. DLQ model

- define a DLQ entry that captures the original task envelope, final error,
  queue, final attempt count, retry policy, and failure timestamps
- decide whether the result backend continues to expose terminal tasks as
  `FAILED` while the DLQ tracks inspection state separately

2. Storage boundary

- introduce a library boundary for writing, listing, and fetching DLQ entries
- keep this separate from broker delivery concerns so inspection works even if
  queue mechanics evolve later

3. Worker integration

- on retry exhaustion, persist the final result as today and also write a DLQ
  entry before acking the broker reservation
- treat DLQ persistence failure as operationally significant and log it clearly

4. API and CLI

- expose app/library methods for listing DLQ IDs and fetching a DLQ entry
- add CLI commands or subcommands for DLQ inspection against Redis-backed state

5. Verification

- cover retry exhaustion in unit tests around worker finalization
- add Redis integration coverage for cross-process DLQ inspection
- verify successful tasks and retryable failures do not create DLQ entries

Acceptance criteria:

- [x] a task that exhausts retries is persisted to the DLQ exactly once
- [x] the DLQ entry contains enough metadata to explain why retries stopped
- [x] a different process can list and inspect the DLQ entry through Redis
- [x] successful tasks and still-retrying tasks never appear in the DLQ
- [x] the existing `result` path still reports terminal state for the task ID

Notes:

- replay currently re-enqueues the dead-lettered task under a fresh task ID
- the original DLQ record is retained for audit and inspection
- replay-resolution metadata and purge workflows remain deferred

Status: `DONE`

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

Delivered:

- Redis-backed broker with ready queues, delayed queues, in-flight reservation,
  `Ack`, and lease-expiry recovery
- Redis-backed result backend with shared cross-process result reads/writes
- backend-selection/config plumbing while keeping in-memory as the default path
- CLI flags for backend selection and Redis connection settings
- integration coverage using separate app instances against a live Redis server
- reproducible local Redis setup via `compose.yml`, `Makefile`, and README docs

Acceptance criteria status:

- [x] a task enqueued by one process can be consumed by a different worker process
- [x] `result` can read task state written by a different worker process
- [x] delayed tasks survive worker restarts
- [x] the in-memory implementations still pass the existing unit tests
- [x] failed tasks retain enough metadata to support DLQ inspection next

Follow-on work:

- DLQ replay-resolution metadata and purge semantics remain deferred
- PostgreSQL and NATS remain deferred
- observability and health endpoints remain later-phase work

Status: `DONE`

### Milestone 8: Idempotency

Prevent duplicate logical tasks from being admitted more than once across
processes.

Problem

Now that Taskforge supports Redis-backed cross-process execution, duplicate
enqueue requests can create multiple task records and multiple broker messages
for what is logically the same job. Milestone 8 closes that correctness gap.

Scope

Add optional enqueue-time idempotency keyed by a caller-supplied string.

Features:

- idempotency key on enqueue
- atomic claim-or-reuse behavior
- shared Redis-backed idempotency store
- in-memory implementation for tests and local parity
- duplicate enqueue returns the canonical existing task ID
- rollback of idempotency claim if enqueue fails
- defined replay behavior for DLQ interaction

Non-goals

Do not include:

- generic distributed task locking
- worker-side exactly-once execution guarantees
- automatic reuse expiry or key TTL policies
- idempotency scopes or partitions beyond a single key string
- operator APIs for manual key release
- PostgreSQL or NATS implementations

Behavior

If `Enqueue` is called without an idempotency key:

- preserve current behavior

If `Enqueue` is called with an idempotency key:

- first caller atomically claims the key and creates a new task
- later callers with the same key do not enqueue a second task
- later callers receive the original task ID

Terminal failures do not free the key automatically.
DLQ replay must use a fresh task identity and must not be blocked by the
original key.

API

Add:

- `taskforge.WithIdempotencyKey(key string)`

Extend the internal task message model to carry:

- `IdempotencyKey string`

Implementation

Add a new internal storage boundary for idempotency records:

- `internal/idempotency/`

Provide:

- memory backend
- Redis backend

Wire it into `App` alongside broker, result, and DLQ.

Enqueue path must:

1. build the task message
2. atomically claim or reuse the idempotency key
3. enqueue only if claim succeeded as new
4. roll back the claim if broker enqueue fails

Acceptance criteria

- [x] two enqueue calls with the same idempotency key return the same task ID
- [x] only one broker message is created for a given idempotency key
- [x] concurrent enqueue attempts from different processes behave correctly with Redis
- [x] enqueue failure does not leave a stale idempotency claim behind
- [x] duplicate enqueue after terminal task failure still returns the original task ID
- [x] enqueue calls without idempotency keys preserve current behavior
- [x] DLQ replay remains usable and is not blocked by the original task's idempotency key
- [x] unit tests cover in-memory behavior and race cases
- [x] Redis integration tests cover cross-process duplicate enqueue

Status: `DONE`

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
