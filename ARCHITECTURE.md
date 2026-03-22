# Taskforge Architecture

Taskforge is a task execution platform written in Go.

At its current stage, Taskforge is an in-process task runtime scaffold designed
to evolve into a distributed, cloud-native task platform.

This document describes the system architecture, execution flow, and the
planned evolution of the platform.

## 1. Goals

Taskforge is designed to explore and implement the core building blocks of a
cloud-native task processing system:

- task registration and dispatch
- asynchronous execution
- worker concurrency
- retry handling
- result storage
- scheduling
- observability
- distributed execution with persistent backends

The long-term goal is to evolve Taskforge from a single-process prototype into
a production-style distributed task platform.

## 2. Current Architecture

At the moment, Taskforge runs as a single-process or demo-oriented system with
in-memory components.

### Core components

#### App

- top-level orchestration object
- wires together task registry, broker, result backend, scheduler, and worker runtime

#### Task Registry

- stores task definitions by name
- maps incoming task messages to executable handlers

#### Broker

- queue abstraction for task delivery
- current implementation is in-memory

#### Worker Runtime

- pulls tasks from the broker
- executes handlers concurrently
- applies timeout, retry, and panic recovery logic

#### Result Backend

- stores execution results and task status
- current implementation is in-memory

#### Scheduler

- supports delayed or periodic task submission
- current implementation is local and process-bound

### Current component diagram

```text
+------------------+
|       App        |
+------------------+
   |    |    |    |
   |    |    |    |
   |    |    |    +-------------------+
   |    |    +----------------------> |
   |    |                             | Scheduler
   |    |    +-------------------+    |
   |    +----------------------> |    |
   |                             | Broker
   |    +-------------------+    |
   +----------------------> |    |
                             | Result Backend
   +-------------------+    |
   +------------------> |    |
                        | Task Registry
                        +-------------------+
```

### Runtime execution view

```text
Client / CLI
    |
    v
 Enqueue Task
    |
    v
  Broker
    |
    v
 Worker Runtime
    |
    v
 Task Handler
    |
    v
 Result Backend
```

## 3. Task Lifecycle

A task moves through a series of states during its lifetime.

### Current lifecycle

```text
PENDING
  |
  v
RUNNING
  |
  +------------------> SUCCESS
  |
  +------------------> FAILED
  |
  +------------------> RETRYING
```

### State descriptions

#### PENDING

- task has been accepted and placed into the broker

#### RUNNING

- task has been picked up by a worker and is actively executing

#### SUCCESS

- task completed successfully and result has been persisted

#### FAILED

- task execution failed and will not be retried anymore

#### RETRYING

- task execution failed but is eligible for another attempt

### Planned lifecycle extensions

As the system evolves, the task state model can expand to include:

- `SCHEDULED`
- `CANCELLED`
- `DEAD_LETTERED`
- `TIMED_OUT`

Example future lifecycle:

```text
SCHEDULED
   |
   v
PENDING
   |
   v
RUNNING
   |
   +-------> SUCCESS
   |
   +-------> RETRYING -> PENDING
   |
   +-------> FAILED
   |
   +-------> DEAD_LETTERED
```

## 4. Worker Execution Flow

The worker runtime is the core of the system.

### Current worker responsibilities

- dequeue messages from the broker
- look up the registered task handler
- execute the handler with task payload
- enforce per-task timeout
- recover from panics
- store result and task status
- requeue tasks on retryable failure

### Current worker flow

1. Dequeue message from broker
2. Mark task as `RUNNING`
3. Resolve handler from registry
4. Execute handler
5. Apply timeout / panic recovery
6. If success:
   persist `SUCCESS` result
7. Else if retryable:
   mark `RETRYING` and re-enqueue
8. Else:
   persist `FAILED` result

### Worker flow diagram

```text
+------------------+
| Dequeue Task     |
+------------------+
          |
          v
+------------------+
| Mark RUNNING     |
+------------------+
          |
          v
+------------------+
| Resolve Handler  |
+------------------+
          |
          v
+------------------+
| Execute Task     |
+------------------+
          |
     +----+----+
     |         |
     v         v
 Success     Failure
     |         |
     v         v
+---------+  +------------------+
| SUCCESS |  | Retry eligible?  |
+---------+  +------------------+
                 |        |
                 | yes    | no
                 v        v
           +----------+  +--------+
           | RETRYING |  | FAILED |
           +----------+  +--------+
                 |
                 v
             Re-enqueue
```

## 5. Current Limitations

The current implementation is intentionally small and useful as a runtime
scaffold, but it has important limitations.

### In-memory broker

The broker is process-local.

Implications:

- tasks do not survive process restarts
- multiple CLI processes do not share queue state
- not suitable for distributed execution

### In-memory result backend

Execution results are also process-local.

Implications:

- results are lost when the process exits
- separate worker, enqueue, and result commands cannot observe shared state

### Process-bound scheduler

Scheduling is local to one running process.

Implications:

- no durable delayed task scheduling
- no distributed coordination
- no failover

### Limited queue semantics

The current queue model is suitable for a prototype but not yet for a
production-style broker.

Missing areas include:

- durable persistence
- acknowledgment protocol
- visibility timeout / lease semantics
- dead-letter queues
- queue prioritization guarantees

## 6. Target Architecture

The next major step is to evolve Taskforge into a multi-process distributed
system backed by persistent infrastructure.

### Target component model

```text
                +--------------------+
                |     API Server     |
                +--------------------+
                          |
                          v
                +--------------------+
                | Persistent Broker  |
                | (Redis / NATS etc) |
                +--------------------+
                   /       |        \
                  /        |         \
                 v         v          v
         +-----------+ +-----------+ +-----------+
         | Worker A  | | Worker B  | | Worker C  |
         +-----------+ +-----------+ +-----------+
                  \        |         /
                   \       |        /
                    v      v       v
                +--------------------+
                |   Result Backend   |
                | (Redis / Postgres) |
                +--------------------+
```

### Responsibilities in the target system

#### API Server

- accepts task submissions
- validates payloads
- writes tasks into the broker
- exposes task lookup APIs
- exposes health and metrics endpoints

#### Persistent Broker

- stores queued tasks durably
- supports multi-process task delivery
- enables workers to compete for tasks safely

#### Worker Fleet

- independently consumes tasks
- executes handlers concurrently
- scales horizontally

#### Result Backend

- stores task status, metadata, and outputs
- allows clients to query historical execution data

## 7. Planned Execution Model

In the distributed design, task execution will become a durable, multi-process
workflow.

### Future execution flow

```text
Client
  |
  v
API Server
  |
  v
Persistent Broker
  |
  v
Worker Fleet
  |
  v
Result Store
  |
  v
Status / Result Query
```

### Future retry model

Instead of immediate in-process retry, retries should be modeled as durable
state transitions.

```text
FAILED ATTEMPT
    |
    v
Compute next retry time
    |
    v
Persist retry metadata
    |
    v
Requeue task after backoff
```

This makes retries:

- observable
- durable
- safe across process restarts

## 8. Persistence Strategy

Persistence is the main architectural boundary between the prototype and the
distributed system.

### Broker options

#### Redis

Pros:

- simple operational model
- good fit for queues
- widely used for job systems

Good first choice for:

- milestone 7
- multi-process task execution
- fast local development

#### PostgreSQL

Pros:

- durable storage
- strong querying capabilities
- useful if tasks and results should live together

Tradeoff:

- queue semantics are more complex than Redis

#### NATS

Pros:

- messaging-native design
- useful for event-driven evolution

Tradeoff:

- larger conceptual jump for the project

### Recommended path

For Taskforge, the best next step is:

- Redis broker
- Redis or PostgreSQL result backend
- later introduce more advanced scheduling / workflow semantics

## 9. Queue Model

A task broker message should eventually contain enough metadata for durable
execution.

### Example message shape

```go
type Message struct {
    ID          string
    TaskName    string
    Payload     []byte
    Queue       string
    Priority    int
    Attempt     int
    MaxRetries  int
    EnqueuedAt  time.Time
    ScheduledAt *time.Time
}
```

### Future queue guarantees to define

Taskforge should eventually document its delivery guarantees explicitly.

Candidate model:

- at-least-once delivery
- task handlers should be idempotent where possible
- retries may result in duplicate execution in failure scenarios

This is a realistic and interview-relevant design choice for distributed
systems.

## 10. Result Model

The result backend should store both execution outcome and metadata useful for
operations.

### Example result shape

```go
type Result struct {
    TaskID      string
    Status      string
    Output      []byte
    Error       string
    Attempt     int
    StartedAt   time.Time
    FinishedAt  time.Time
}
```

### Why this matters

A richer result model allows:

- task history lookup
- failure debugging
- latency measurement
- observability integration
- future dashboard support

## 11. Observability Architecture

As Taskforge evolves, observability becomes a first-class concern.

### Logging

Use structured logs for:

- task enqueue events
- task start / finish
- retries
- failures
- worker lifecycle

Recommended libraries:

- `log/slog`
- `zap`

### Metrics

Expose metrics such as:

- queue depth
- tasks started
- tasks completed
- tasks failed
- task duration
- retry count
- worker concurrency utilization

Recommended stack:

- Prometheus
- Grafana

### Tracing

Trace a task from submission to completion.

Example trace path:

```text
enqueue -> broker -> dequeue -> handler -> result persist
```

Recommended stack:

- OpenTelemetry

## 12. Deployment Evolution

Taskforge should evolve in deployment stages.

### Stage 1: In-process prototype

- single binary
- in-memory broker
- local development only

### Stage 2: Multi-process local stack

- separate API and worker processes
- Redis-backed broker
- Docker Compose for local orchestration

### Stage 3: Cloud-native deployment

- container images
- Kubernetes deployments
- health probes
- autoscaling workers

## 13. Design Principles

Taskforge should follow a few key architectural principles.

### Clear interface boundaries

Broker, scheduler, and result backend should remain interface-driven so
implementations can be swapped without rewriting the runtime.

### Small core, extensible edges

Keep the runtime small and focused. Add complexity at boundaries such as
persistence, scheduling, and observability.

### Explicit delivery semantics

Document guarantees clearly rather than implying stronger guarantees than the
implementation provides.

### Idempotency-friendly execution

Assume retries and duplicates are possible in distributed environments.

### Cloud-native progression

Do not over-claim maturity early. Let the architecture evolve in visible
stages.

## 14. Near-Term Priorities

The current highest-priority architectural step is persistence.

### Next recommended milestone

#### Milestone 7: Persistence Layer

Status: implemented for the Redis path.

Recommended implementation order:

- introduce Redis-backed broker
- make worker, enqueue, and result share real state
- add durable retry metadata
- add dead-letter queue support
- add metrics and health endpoints

This milestone turns Taskforge from a runtime demo into a real multi-process
system.

Implemented shape:

- `pkg/taskforge.Config` now supports backend selection and Redis connection
  settings
- `internal/result.RedisBackend` provides shared result storage across app
  instances
- `internal/broker.RedisBroker` provides ready queues, delayed queues,
  in-flight reservation, `Ack`, and lease-expiry recovery
- live Redis integration tests verify separate app instances can enqueue,
  process, and read results through shared state

Planning notes that remain useful for follow-on milestones:

- `internal/broker` already defines the transport boundary; add `RedisBroker`
  beside `MemoryBroker` rather than changing the interface first
- `internal/result` already defines the result storage boundary; add
  `RedisBackend` with the same `SetResult` and `GetResult` contract
- `pkg/taskforge.App` currently hardwires memory backends in `New`; add config
  and constructors so backend selection happens at app construction time
- `internal/worker` can stay mostly unchanged if broker dequeue semantics remain
  blocking and retries continue to be represented as re-enqueued `task.Message`
- `internal/task.Message` already contains retry and scheduling metadata, so the
  first persistence pass should preserve this schema and avoid a larger task
  model redesign

Recommended scope split:

1. Wiring

- extend `taskforge.Config` with backend choice and Redis connection settings
- add constructors for memory and Redis-backed apps
- keep the current default as in-memory so existing tests and examples stay
  stable

2. Redis result backend

- store results by task ID
- preserve TTL behavior where configured
- support `PENDING`, `RUNNING`, `RETRYING`, `SUCCESS`, and `FAILED` states
- ensure serialized results are readable across separate processes

3. Redis broker

- immediate queue for ready tasks
- delayed queue or sorted-set schedule for future tasks
- blocking dequeue for workers
- explicit ack path, even if the first Redis implementation uses a simpler
  reservation model

4. Retry and failure durability

- persist incremented attempt counts in the broker payload
- keep failure error text and timestamps in the result backend
- leave a clear hook for moving terminal failures into a DLQ keyspace

5. Verification

- add integration coverage for separate enqueue, worker, and result app
  instances sharing Redis
- verify delayed tasks and retries continue after worker restart
- document the operational constraint shift in `README.md`

Non-goals for the first milestone 7 cut:

- PostgreSQL and NATS support
- metrics and health endpoints
- full DLQ inspection CLI
- task deduplication and locking

## 15. Summary

Taskforge currently provides:

- a task registry
- a worker runtime
- retry-aware execution
- scheduler scaffolding
- broker and result abstractions

Taskforge does not yet provide:

- durable queueing
- shared multi-process state
- production-grade observability
- cloud-native deployment

The architecture is intentionally staged:

- start with an in-process runtime
- introduce persistence
- expand into distributed execution
- add observability and deployment primitives
- evolve toward workflow semantics

That staged evolution is the core design story of the project.
