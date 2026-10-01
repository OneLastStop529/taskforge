# Taskforge Architecture / Taskforge 架构说明

> 中文说明：本文介绍 Taskforge 的架构目标、核心组件、执行流程和演进路线。请区分当前实现与后续增强规划；提交幂等已支持内存和 Redis 后端。

Taskforge is a task execution platform written in Go.

At its current stage, Taskforge is a prototype task runtime that still defaults
to in-process execution, but now also supports Redis-backed multi-process task
delivery, result storage, DLQ inspection, and enqueue-time idempotency.

This document describes the system architecture, execution flow, and the
planned evolution of the platform.

## 1. Goals / 1. 设计目标

> 中文说明：探索云原生任务系统的基本组成：任务注册与分发、异步执行、并发工作池、重试、结果存储、调度、可观测性，以及基于持久化后端的分布式执行。长期目标是从单进程原型逐步演进为具备生产系统特征的分布式任务平台。

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

## 2. Current Architecture / 2. 当前架构

> 中文说明：当前支持单进程内存模式和 Redis 多进程模式；App 组装队列、结果、死信、提交幂等、注册表及调度组件。

Taskforge now supports two operating modes:

- in-process/demo-oriented execution with in-memory backends
- multi-process execution with Redis-backed broker, result, DLQ, and
  idempotency backends

### Core components / 核心组件

> 中文说明：App 负责组装组件；任务注册表决定执行什么，Broker 决定如何交付任务，Worker 负责执行，Result Backend 保存结果，Scheduler 负责定期提交。当前另有独立的 DLQ Backend 保存永久失败的任务记录。

#### App / 应用对象

> 中文说明：顶层编排入口，连接任务注册表、队列、结果后端、死信后端、调度器和工作池，并提供统一的公开 API。

- top-level orchestration object
- wires together task registry, broker, result backend, DLQ backend,
  idempotency backend, scheduler, and worker runtime

#### Task Registry / 任务注册表

> 中文说明：按任务名称保存处理函数，将收到的任务消息映射到可执行的 Go 函数。工作进程需要注册它要执行的任务。

- stores task definitions by name
- maps incoming task messages to executable handlers

#### Broker / 任务队列

> 中文说明：抽象任务的入队、出队与确认操作。当前既有进程内实现，也有 Redis 实现；后者支持就绪队列、延迟队列和执行中任务预留。

- queue abstraction for task delivery
- implementations: memory and Redis

#### Worker Runtime / 工作池运行时

> 中文说明：从队列中获取消息，并发调用处理函数，传递超时上下文，处理重试并捕获 panic。

- pulls tasks from the broker
- executes handlers concurrently
- applies timeout, retry, and panic recovery logic

#### Result Backend / 结果存储后端

> 中文说明：保存任务状态、执行输出及错误等元数据。当前支持内存和 Redis 后端；Redis 使不同进程能够查询共享的任务结果。

- stores execution results and task status
- implementations: memory and Redis

#### DLQ Backend / 死信后端

> 中文说明：保存最终失败的任务及检查、重放所需信息，支持内存和 Redis。

- stores terminal failure inspection records
- implementations: memory and Redis

#### Idempotency Backend / 提交幂等后端

> 中文说明：按调用方提供的键认领或复用任务 ID，支持内存和 Redis。

- stores enqueue-time claim-or-reuse records keyed by caller-supplied strings
- implementations: memory and Redis

#### Scheduler / 调度器

> 中文说明：当前周期调度器运行于本地进程，按固定间隔提交任务。单次延迟任务通过消息中的计划执行时间交由 Broker 处理；两者的持久化边界不同。

- submits periodic tasks at fixed intervals; the broker handles one-off delays
- current implementation is local and process-bound

### Current component diagram / 当前组件关系图

> 中文说明：下图说明 App 与主要组件的组装关系。图中包含死信与提交幂等存储组件。

```text
+------------------+
|       App        |
+------------------+
   |    |    |    |    |    |
   |    |    |    |    |    +-------------------+
   |    |    |    |    +----------------------> |
   |    |    |    |                             | Scheduler
   |    |    |    +-------------------+         |
   |    |    +----------------------> |         |
   |    |                             | Broker
   |    +-------------------+         |
   +----------------------> |         |
                             | Result Backend
   +-------------------+    |
   +------------------> |    |
                        | Task Registry
   +-------------------+    |
   +------------------> |    |
                        | DLQ Backend
   +-------------------+    |
   +------------------> |    |
                        | Idempotency Backend
                        +-------------------+
```

### Runtime execution view / 运行时执行视图

> 中文说明：客户端或 CLI 提交任务后，消息进入 Broker，由 Worker 调用对应处理函数，最终将状态和输出写入结果后端。

```text
Client / CLI
    |
    v
 Enqueue Task
    |
    v
 Idempotency Backend
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

Terminal failure --> DLQ Backend
```

## 3. Task Lifecycle / 3. 任务生命周期

> 中文说明：任务通过状态表示等待、执行、成功、重试或最终失败。结果存储记录的是任务状态，不应将其视为完整的历史事件日志。

A task moves through a series of states during its lifetime.

### Current lifecycle / 当前状态流转

> 中文说明：任务通常从 PENDING 进入 RUNNING，再转为 SUCCESS、FAILED 或 RETRYING。重试消息重新进入队列后，还会再次进入 RUNNING。

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

### State descriptions / 状态说明

> 中文说明：下面保留程序中的英文状态常量，并补充中文含义，便于与日志和查询结果对照。

#### PENDING / 等待执行

> 中文说明：任务已被接收并放入队列，等待工作进程处理。

- task has been accepted and placed into the broker

#### RUNNING / 执行中

> 中文说明：工作进程已取得任务，正在解析并执行相应处理函数。

- task has been picked up by a worker and is actively executing

#### SUCCESS / 执行成功

> 中文说明：处理函数成功返回；工作进程随后尝试保存结果并确认队列消息。

- handler completed successfully; the worker attempts to persist the result

#### FAILED / 最终失败

> 中文说明：任务失败且不再重试。当前还会尝试保存死信记录，供后续检查和重放。

- task execution failed and will not be retried anymore

#### RETRYING / 等待重试

> 中文说明：本次执行失败，但仍有剩余尝试次数；系统计算退避时间并重新提交任务。

- task execution failed but is eligible for another attempt

### Planned lifecycle extensions / 计划中的状态扩展

> 中文说明：SCHEDULED、CANCELLED、DEAD_LETTERED 和 TIMED_OUT 是这里讨论的未来状态。当前死信记录独立保存，公开任务结果仍为 FAILED；图中的扩展状态不代表已经实现。

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

## 4. Worker Execution Flow / 4. 工作池执行流程

> 中文说明：工作池负责把队列消息转换为处理函数调用，并根据执行结果决定确认、重试或保存最终失败记录。

The worker runtime is the core of the system.

### Current worker responsibilities / 当前工作池职责

> 中文说明：主要职责包括取消息、查找处理函数、执行任务、传递超时上下文、捕获 panic、保存状态以及重新提交可重试任务。超时不是强制终止 Go 函数，需要处理函数响应 context。

- dequeue messages from the broker
- look up the registered task handler
- execute the handler with task payload
- pass a per-task context deadline for cooperative cancellation
- recover from panics
- store result and task status
- requeue tasks on retryable failure

### Current worker flow / 当前执行步骤

> 中文说明：获取消息后写入 RUNNING，查找并执行处理函数。成功时写入 SUCCESS 并确认消息；可重试时写入 RETRYING，提交延迟重试消息后确认原消息；耗尽尝试次数时写入 FAILED，尝试保存死信记录，再确认消息。

1. Dequeue a message and acquire worker capacity.
2. Attempt to write `RUNNING` and resolve the handler.
3. Create a timeout context if configured, then execute with panic recovery.
4. On success: attempt to save `SUCCESS`, then acknowledge.
5. On retryable failure: attempt to save `RETRYING`, enqueue the next attempt,
   then acknowledge the original delivery if enqueue succeeded.
6. On terminal failure: attempt to save `FAILED` and the DLQ entry, then acknowledge.

Result writes, DLQ writes and acknowledgment are separate operations. Some write
errors are ignored or only logged; this is not an atomic completion protocol.

结果、死信和确认是独立操作；部分写入错误仅被忽略或记录日志，目前不具备原子完成协议。

### Worker flow diagram / 工作池流程图

> 中文说明：图中展示执行成功与失败后的分支。当前最终失败分支还包含死信存储步骤，重试分支会根据策略设置下次执行时间。

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

## 5. Current Limitations / 5. 当前限制

> 中文说明：本节区分内存后端限制与当前运行时的可靠性缺口。内存数据仍随进程退出而丢失，但 Redis 已提供共享队列、延迟消息、确认及租约恢复机制；这并不等于系统已具备完整的生产可靠性保障。

The current implementation is intentionally small and useful as a runtime
scaffold, but it has important limitations.

### In-memory broker / 内存队列

> 中文说明：队列仅存在于当前进程中，重启后任务丢失，多个 CLI 进程无法共享队列。该限制适用于内存后端。

The broker is process-local.

Implications:

- tasks do not survive process restarts
- multiple CLI processes do not share queue state
- not suitable for distributed execution

### In-memory result backend / 内存结果后端

> 中文说明：结果也仅保存在当前进程中，退出后丢失。分别启动的 worker、enqueue 和 result 需要使用共享后端才能观察同一批任务。

Execution results are also process-local.

Implications:

- results are lost when the process exits
- separate worker, enqueue, and result commands cannot observe shared state

### Process-bound scheduler / 进程内调度器

> 中文说明：周期调度配置和运行状态仍在本地进程中，没有分布式协调或故障转移。Redis Broker 已支持保存单次延迟任务，因此应将延迟消息与周期调度器的持久化区分开来。

Scheduling is local to one running process.

Implications:

- periodic schedule definitions and next-run state are not persisted
- one-off delayed tasks are stored by the Redis broker when selected
- no distributed coordination
- no failover

### Limited queue semantics / 队列语义限制

> 中文说明：Redis 已支持确认和租约恢复；提交幂等也已实现。任务锁、租约续期和更强的失败一致性仍需增强。

The current queue model is suitable for a prototype but not yet for a
production-style broker.

Remaining gaps include:

- renewable leases and unique delivery receipts (current leases default to 30 seconds)
- atomic enqueue with idempotency claim, and atomic retry/completion transitions
- protection against stale workers and duplicate handler side effects
- queue prioritization guarantees

Redis already provides shared ready/delayed queues, reservations, acknowledgment
and expiry recovery. DLQ storage is implemented separately. Redis restart durability
depends on server configuration; these features do not imply exactly-once execution.

## 6. Target Architecture / 6. 目标架构

> 中文说明：目标是在现有 Redis 多进程能力之上，引入 API 服务、可扩展的工作进程集群和更完善的运维能力。图中的 API 服务及其他候选存储不代表当前已有实现。

The target extends the existing Redis-backed multi-process runtime with an API
service, stronger failure recovery, observability and deployment automation.

### Target component model / 目标组件模型

> 中文说明：API 服务接收请求并写入持久化队列，多个 Worker 竞争消费任务，再将结果写入共享存储。Redis 已有实现，NATS 和 PostgreSQL 仍属于后续选项。

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

### Responsibilities in the target system / 目标系统中的职责

> 中文说明：通过明确组件边界，将请求接入、任务交付、任务执行和结果查询分开，以便独立扩展。

#### API Server / API 服务

> 中文说明：计划负责接收任务、校验输入、写入队列，提供任务查询、健康检查和指标接口；当前主要入口仍是 Go 库与 CLI。

- accepts task submissions
- validates payloads
- writes tasks into the broker
- exposes task lookup APIs
- exposes health and metrics endpoints

#### Persistent Broker / 持久化队列

> 中文说明：目标是保存待执行任务，为多进程提供共享交付机制，并支持多个 Worker 协作消费。数据能否抵御 Redis 服务重启还取决于 Redis 本身的持久化配置。

- stores queued tasks durably
- supports multi-process task delivery
- enables workers to compete for tasks safely

#### Worker Fleet / 工作进程集群

> 中文说明：多个独立 Worker 消费任务、并发执行处理函数，并按负载进行水平扩展。

- independently consumes tasks
- executes handlers concurrently
- scales horizontally

#### Result Backend / 结果存储后端

> 中文说明：保存任务状态、执行输出及错误等元数据。当前支持内存和 Redis 后端；Redis 使不同进程能够查询共享的任务结果。

- stores task status, metadata, and outputs
- allows clients to query historical execution data

## 7. Planned Execution Model / 7. 计划中的执行模型

> 中文说明：整体目标是让任务跨进程执行，并将消息、重试信息和结果放入共享存储。当前已实现 Redis 路径，完整 API 服务流程仍是规划。

In the distributed design, task execution will become a durable, multi-process
workflow.

### Future execution flow / 未来执行流程

> 中文说明：客户端通过 API 服务提交任务，持久化队列将消息交给工作进程集群，执行结果进入共享存储，供客户端查询状态和输出。

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

### Future retry model / 未来重试模型

> 中文说明：重试应表示为可存储的状态与消息：计算下次时间，记录尝试信息，经过退避后再次入队。当前 Redis 路径已经在消息中保存尝试次数与计划时间，但完整的故障一致性仍需持续完善。

Retries already use delayed messages with attempt metadata. The next improvement
is to make retry publication and acknowledgment a recoverable atomic transition.

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

## 8. Persistence Strategy / 8. 持久化策略

> 中文说明：持久化后端是从单进程原型走向分布式执行的重要边界。当前选择 Redis，其他方案仍作为后续设计选项。

Persistence is the main architectural boundary between the prototype and the
distributed system.

### Broker options / 队列后端选项

> 中文说明：下面比较 Redis、PostgreSQL 和 NATS 的设计取向；这不是当前已支持后端的完整功能清单。

#### Redis / Redis 后端

> 中文说明：运维模型相对简单，适合任务队列和快速本地开发。项目已将其用于共享任务队列、结果存储与死信存储。

Pros:

- simple operational model
- good fit for queues
- widely used for job systems

Good first choice for:

- milestone 7
- multi-process task execution
- fast local development

#### PostgreSQL / PostgreSQL 后端

> 中文说明：适合需要持久存储、复杂查询或希望将任务与结果放在同一数据库中的场景；但需要自行设计队列领取与并发控制语义。当前尚未实现。

Pros:

- durable storage
- strong querying capabilities
- useful if tasks and results should live together

Tradeoff:

- queue semantics are more complex than Redis

#### NATS / NATS 消息系统

> 中文说明：以消息传递为核心，适合向事件驱动系统演进；引入它也会增加新的概念和运维要求。当前尚未实现。

Pros:

- messaging-native design
- useful for event-driven evolution

Tradeoff:

- larger conceptual jump for the project

### Recommended path / 建议演进路线

> 中文说明：Redis 队列和结果后端已落地，下一步可完善可靠性、调度与工作流语义。PostgreSQL 结果后端仍是可选的未来方向。

Redis broker and result backends are implemented. Next steps are reliability
hardening and observability; PostgreSQL, advanced scheduling and workflows remain
future options.

## 9. Queue Model / 9. 队列消息模型

> 中文说明：消息需要携带任务标识、处理函数名称、负载、队列、重试与调度信息，以支持跨进程交付和恢复。

Task messages already carry retry, scheduling and idempotency metadata.

### Example message shape / 消息结构示意

> 中文说明：下方摘录当前 internal/task/task.go 中的 Message，包含重试策略、计划时间与提交幂等键。

```go
type Message struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Payload        json.RawMessage `json:"payload"`
	Queue          string          `json:"queue"`
	Priority       int             `json:"priority"`
	Attempt        int             `json:"attempt"`
	RetryPolicy    RetryPolicy     `json:"retry_policy"`
	ScheduledAt    time.Time       `json:"scheduled_at"`
	EnqueuedAt     time.Time       `json:"enqueued_at"`
	Timeout        time.Duration   `json:"timeout"`
	IdempotencyKey string          `json:"idempotency_key,omitempty"`
}
```

### Future queue guarantees to define / 待明确的交付保证

> 中文说明：设计应考虑至少一次交付下的重复执行：重试或租约恢复都可能使任务再次运行。处理函数应尽量保持幂等，项目尚未实现完整的去重与任务锁机制。

Taskforge should eventually document its delivery guarantees explicitly.

Candidate model:

- at-least-once delivery
- task handlers should be idempotent where possible
- retries may result in duplicate execution in failure scenarios

This is a realistic and interview-relevant design choice for distributed
systems.

## 10. Result Model / 10. 结果模型

> 中文说明：结果后端既保存执行输出，也保存错误、尝试次数和时间戳，便于查询与排障。

The result backend should store both execution outcome and metadata useful for
operations.

### Example result shape / 结果结构示意

> 中文说明：下方摘录当前 internal/task/task.go 中的 Result，保存最新结果与执行元数据。

```go
type Result struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	State      State           `json:"state"`
	Output     json.RawMessage `json:"output,omitempty"`
	Error      string          `json:"error,omitempty"`
	Attempt    int             `json:"attempt"`
	StartedAt  time.Time       `json:"started_at"`
	FinishedAt time.Time       `json:"finished_at"`
}
```

### Why this matters / 结果元数据的价值

> 中文说明：丰富的元数据有助于结果查询、失败排查、耗时分析、可观测性集成及未来的管理界面；完整历史记录仍需要额外设计。

A richer result model allows:

- task history lookup
- failure debugging
- latency measurement
- observability integration
- future dashboard support

## 11. Observability Architecture / 11. 可观测性架构

> 中文说明：本节描述计划中的日志、指标和追踪体系。当前运行时已有普通日志输出，但尚未实现这里列出的完整可观测性方案。

As Taskforge evolves, observability becomes a first-class concern.

### Logging / 日志

> 中文说明：计划以结构化日志记录入队、开始、结束、重试、失败和 Worker 生命周期事件，可考虑 log/slog 或 zap。

Use structured logs for:

- task enqueue events
- task start / finish
- retries
- failures
- worker lifecycle

Recommended libraries:

- `log/slog`
- `zap`

### Metrics / 指标

> 中文说明：计划统计队列深度、任务开始与完成数、失败数、执行时长、重试次数和工作池利用率，并通过 Prometheus 与 Grafana 采集和展示。

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

### Tracing / 链路追踪

> 中文说明：计划使用 OpenTelemetry，将提交、入队、取出、执行和结果保存串联为可追踪的任务路径。

Trace a task from submission to completion.

Example trace path:

```text
enqueue -> broker -> dequeue -> handler -> result persist
```

Recommended stack:

- OpenTelemetry

## 12. Deployment Evolution / 12. 部署演进

> 中文说明：按阶段从单进程演示发展到本地多进程，再引入容器化、健康检查和自动扩缩容。

Taskforge should evolve in deployment stages.

### Stage 1: In-process prototype / 阶段 1：进程内原型

> 中文说明：单个可执行程序使用内存队列，适合本地开发与功能验证。

- single binary
- in-memory broker
- local development only

### Stage 2: Multi-process local stack / 阶段 2：本地多进程环境

> 中文说明：通过 Redis 连接独立进程。当前 Compose 文件用于启动 Redis；独立 API 服务和完整服务编排仍属于目标形态。

- separate API and worker processes
- Redis-backed broker
- Docker Compose for local orchestration

### Stage 3: Cloud-native deployment / 阶段 3：云原生部署

> 中文说明：后续提供容器镜像、Kubernetes 部署、健康探针，以及基于负载的 Worker 自动扩缩容。

- container images
- Kubernetes deployments
- health probes
- autoscaling workers

## 13. Design Principles / 13. 设计原则

> 中文说明：保持组件职责清楚，以可验证的阶段逐步增强系统能力。

Taskforge should follow a few key architectural principles.

### Clear interface boundaries / 清晰的接口边界

> 中文说明：通过 Broker、结果存储及调度相关抽象隔离实现细节，让更换后端尽量不影响核心运行时。

Broker, scheduler, and result backend should remain interface-driven so
implementations can be swapped without rewriting the runtime.

### Small core, extensible edges / 精简核心，扩展边界

> 中文说明：保持任务运行时小而专注，将持久化、调度和可观测性的复杂性放在对应组件中。

Keep the runtime small and focused. Add complexity at boundaries such as
persistence, scheduling, and observability.

### Explicit delivery semantics / 明确交付语义

> 中文说明：准确说明已实现的保证，避免把设计目标当作实际能力，尤其要区分确认、恢复和恰好执行一次。

Document guarantees clearly rather than implying stronger guarantees than the
implementation provides.

### Idempotency-friendly execution / 面向幂等的执行设计

> 中文说明：默认考虑重试与重复执行的可能性，任务处理逻辑应能安全应对相同工作被再次提交。

Assume retries and duplicates are possible in distributed environments.

### Cloud-native progression / 渐进式云原生演进

> 中文说明：通过可见、可验证的阶段推进架构，不提前宣称尚未达到的成熟度。

Do not over-claim maturity early. Let the architecture evolve in visible
stages.

## 14. Recent Milestones And Next Work / 14. 近期里程碑与后续工作

Redis-backed persistence, DLQ support, and enqueue idempotency are now
implemented for the current architecture.

### Recently completed milestones / 近期完成的里程碑

> 中文说明：Redis 持久化、死信队列与提交幂等已落地；下文回顾设计及实现范围。

#### Milestone 6: Dead Letter Queue / 里程碑 6：死信队列

> 中文说明：已支持内存和 Redis 死信存储、查询、重放与单条清除。重放创建新 ID，保留原记录并更新重放元数据。

Status: implemented for memory and Redis backends.

Delivered:

- memory and Redis DLQ backends with list/get/replay/purge APIs and CLI commands
- terminal `FAILED` results plus separate DLQ inspection records
- replay under a fresh task ID, retaining the original record and recording replay
  count, last replay ID and timestamp
- unit and live Redis tests using separate App instances

Bulk purge and richer replay-resolution workflows remain future work. DLQ writes
are not atomic with result persistence and broker acknowledgment.

批量清除及更丰富的重放处理流程仍待实现；死信写入、结果保存和消息确认目前不是原子操作。

#### Milestone 7: Persistence Layer / 里程碑 7：持久化层

> 中文说明：Redis 已提供共享队列与结果存储、延迟消息、预留确认和租约恢复。PostgreSQL、NATS 及完整故障一致性仍待实现。

Status: implemented for the Redis path.

- `Config` selects memory or Redis backends when `App` is constructed.
- Redis broker stores ready/delayed messages and supports in-flight reservations,
  acknowledgment and lease-expiry recovery.
- Redis results preserve the configured TTL and are shared across App instances.
- Compose starts Redis only; an API service and full deployment stack are not present.
- Integration tests cover shared state and delayed work across worker restarts using
  separate App instances in one test process.

Redis persistence depends on the Redis server configuration. PostgreSQL and NATS
are unimplemented; metrics and health endpoints belong to later milestones.

#### Milestone 8: Idempotency / 里程碑 8：幂等性

> 中文说明：已支持内存及 Redis 提交键去重，同键返回原任务 ID；失败不自动释放键，死信重放使用新身份。执行锁和键过期策略不属于当前实现。

Status: implemented for memory and Redis backends.

Implemented shape:

- `taskforge.WithIdempotencyKey(...)` adds optional enqueue-time deduplication
- `internal/idempotency` defines the storage boundary with memory and Redis
  backends
- duplicate enqueue returns the canonical existing task ID instead of
  enqueuing a second logical task
- enqueue rollback releases the idempotency claim if broker admission fails
- DLQ replay clears the original idempotency key so replay remains usable

Verification:

- package tests cover canonical reuse, rollback, and in-process race behavior
- Redis integration tests cover duplicate enqueue and replay across separate App
  instances in one process; independent-process crash testing remains future work

### Next recommended milestone / 下一步建议

> 中文说明：下一阶段建议实现结构化日志，为排查重试、租约恢复与幂等复用提供清晰事件记录。

#### Milestone 9: Structured Logging / 里程碑 9：结构化日志

Status: next open milestone.

Why this is the highest-ROI next step:

- the runtime now has enough durable state and operational branches that
  human-readable debugging through ad hoc logs is starting to break down
- worker retries, DLQ transitions, Redis dequeue/reservation behavior, and
  idempotent enqueue reuse all benefit from structured event logs
- logging is a lower-risk observability step than metrics or tracing and will
  make later milestone verification easier

## 15. Summary / 15. 小结

Taskforge currently provides:

- a task registry
- a worker runtime
- retry-aware execution
- scheduler scaffolding
- DLQ persistence, inspection, replay, and purge
- Redis-backed multi-process execution
- enqueue-time idempotency
- broker and result abstractions

Taskforge does not yet provide:

- atomic claim-and-enqueue or retry/completion transactions
- renewable worker leases or exactly-once external side effects
- production-grade observability
- cloud-native deployment

The architecture is intentionally staged:

- start with an in-process runtime
- introduce persistence
- expand into distributed execution
- add observability and deployment primitives
- evolve toward workflow semantics

That staged evolution is the core design story of the project.
