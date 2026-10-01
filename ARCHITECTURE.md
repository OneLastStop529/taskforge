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

#### DLQ Backend

- stores terminal failure inspection records
- implementations: memory and Redis

#### Idempotency Backend

- stores enqueue-time claim-or-reuse records keyed by caller-supplied strings
- implementations: memory and Redis

#### Scheduler / 调度器

> 中文说明：当前周期调度器运行于本地进程，按固定间隔提交任务。单次延迟任务通过消息中的计划执行时间交由 Broker 处理；两者的持久化边界不同。

- supports delayed or periodic task submission
- current implementation is local and process-bound

### Current component diagram / 当前组件关系图

> 中文说明：下图说明 App 与主要组件的组装关系。图中尚未包含当前已实现的独立死信存储组件。

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

- task completed successfully and result has been persisted

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
- enforce per-task timeout
- recover from panics
- store result and task status
- requeue tasks on retryable failure

### Current worker flow / 当前执行步骤

> 中文说明：获取消息后写入 RUNNING，查找并执行处理函数。成功时写入 SUCCESS 并确认消息；可重试时写入 RETRYING，提交延迟重试消息后确认原消息；耗尽尝试次数时写入 FAILED，尝试保存死信记录，再确认消息。

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

> 中文说明：本节部分限制描述的是早期内存原型。内存数据仍随进程退出而丢失，但 Redis 已提供共享队列、延迟消息、确认及租约恢复机制；这并不等于系统已具备完整的生产可靠性保障。

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

- no durable delayed task scheduling
- no distributed coordination
- no failover

### Limited queue semantics / 队列语义限制

> 中文说明：Redis 已支持确认和租约恢复；提交幂等也已实现。任务锁、租约续期和更强的失败一致性仍需增强。

The current queue model is suitable for a prototype but not yet for a
production-style broker.

Missing areas include:

- durable persistence
- acknowledgment protocol
- visibility timeout / lease semantics
- dead-letter queues
- queue prioritization guarantees

## 6. Target Architecture / 6. 目标架构

> 中文说明：目标是在现有 Redis 多进程能力之上，引入 API 服务、可扩展的工作进程集群和更完善的运维能力。图中的 API 服务及其他候选存储不代表当前已有实现。

The next major step is to evolve Taskforge into a multi-process distributed
system backed by persistent infrastructure.

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

For Taskforge, the best next step is:

- Redis broker
- Redis or PostgreSQL result backend
- later introduce more advanced scheduling / workflow semantics

## 9. Queue Model / 9. 队列消息模型

> 中文说明：消息需要携带任务标识、处理函数名称、负载、队列、重试与调度信息，以支持跨进程交付和恢复。

A task broker message should eventually contain enough metadata for durable
execution.

### Example message shape / 消息结构示意

> 中文说明：下方是概念示例，不是当前 Go 类型的逐字段定义。实际字段以 internal/task/task.go 为准，例如当前使用 Name、RetryPolicy 和 time.Time 类型的 ScheduledAt。

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

> 中文说明：下方类型用于解释概念。当前实际结构使用 ID、State 和 json.RawMessage 类型的 Output 等字段，应以 internal/task/task.go 为准。

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

Why this is the highest-ROI next step:

- the Redis broker and result backend already preserve shared state
- retries are implemented, so the next gap is operator handling of permanent
  failure
- DLQ support improves reliability immediately without forcing a larger
  execution-model redesign

Implemented milestone 6 shape:

1. Add a DLQ storage boundary

- introduce a dedicated interface for DLQ persistence and inspection rather
  than folding dead-letter data into the broker API
- keep broker responsibilities focused on delivery and reservation semantics
- keep result backend responsibilities focused on latest task outcome lookup

2. Define the first DLQ entry shape

- store the original `task.Message` payload so operators can inspect what ran
- add final failure fields such as terminal error text, exhausted attempt
  number, and failure time
- include queue and retry policy metadata so later replay tooling does not need
  to reconstruct context from logs

3. Wire worker finalization to DLQ persistence

- on success: write `SUCCESS` result and ack as today
- on retryable failure: write `RETRYING`, re-enqueue, and do not touch the DLQ
- on retry exhaustion: write the terminal result, persist the DLQ entry, then
  ack the broker reservation

4. Expose operator inspection and replay surfaces

- add library methods to list DLQ entry IDs and fetch a single entry
- add CLI inspection commands once the library contract is stable
- support replay by re-enqueuing the stored task envelope under a fresh task ID
- defer replay-resolution metadata, bulk purge, and richer requeue workflows

5. Verify with the existing Redis integration model

- reuse the separate producer / worker / inspector app pattern already used for
  Redis result integration tests
- prove terminal failures become visible to another process through the DLQ
  backend
- prove successful and still-retrying tasks never produce DLQ entries
- prove a different process can replay a DLQ entry through shared Redis state

Key design choices in the current cut:

- keep the public task result state as `FAILED` on terminal failure and treat
  the DLQ as an additional inspection record, not a replacement result path
- replay allocates a new task ID instead of mutating the terminally failed task
- retain the original DLQ record after replay for audit and inspection
- defer introducing a public `DEAD_LETTERED` runtime state until the project
  needs distinct operator semantics beyond terminal failure lookup

#### Milestone 7: Persistence Layer / 里程碑 7：持久化层

> 中文说明：Redis 实现提供共享结果和就绪、延迟、执行中队列，以及确认和租约恢复；PostgreSQL 和 NATS 尚未实现。

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
- Redis integration tests cover cross-process duplicate enqueue and replay
  interaction

### Next recommended milestone / 下一步建议

> 中文说明：下一阶段建议实现结构化日志，为排查重试、租约恢复与幂等复用提供清晰事件记录。

#### Milestone 9: Structured Logging

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
