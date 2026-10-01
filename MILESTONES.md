# Taskforge Milestones / Taskforge 里程碑

> 中文说明：本文记录从 Go 进程内任务原型到分布式云原生平台的演进路线。DONE 表示该里程碑约定范围已交付，TODO 表示尚未整体完成；不代表其中所有基础能力都未实现。

Taskforge is a cloud-native task execution platform written in Go.

This document tracks the roadmap from an in-process prototype to a distributed,
cloud-native task platform.

## Phase 1: Core Runtime (Milestones 1-4) / 阶段 1：核心运行时（里程碑 1–4）

> 中文说明：建立基本任务执行能力，包括项目结构、命令行入口、任务模型和并发工作池。

Goal: Build the basic task execution runtime.

### Milestone 1: Project Structure ✅ / 里程碑 1：项目结构 ✅

> 中文说明：已建立 Go 模块和代码目录，按命令入口、内部组件与公开 API 划分职责。下方目录树为结构示意。

Create a clean Go project layout.

Features: / 功能

- Go module
- repo structure

Example layout: / 结构示例

```text
cmd/
internal/
pkg/
configs/
```

Status / 状态: `DONE`（已完成）

### Milestone 2: CLI Interface ✅ / 里程碑 2：命令行接口 ✅

> 中文说明：已提供 worker、enqueue、result 和 demo 命令。demo 在同一进程内运行完整示例；独立命令之间共享状态需要配置 Redis 后端。

Provide CLI commands to interact with the system.

Commands: / 命令

- `taskforge worker`
- `taskforge enqueue`
- `taskforge result`
- `taskforge demo`

Status / 状态: `DONE`（已完成）

### Milestone 3: Task Model ✅ / 里程碑 3：任务模型 ✅

> 中文说明：已实现任务注册表、负载处理、任务元数据和结果模型。下方类型为设计示意，实际字段以 internal/task/task.go 为准。

Implement the core task abstraction.

Features: / 功能

- task registry
- payload handling
- task metadata
- result model

Example: / 示例

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

Status / 状态: `DONE`（已完成）

### Milestone 4: Worker Runtime ✅ / 里程碑 4：工作池运行时 ✅

> 中文说明：已实现基于 goroutine 的并发工作池、panic 恢复、任务超时上下文及重试执行。超时依赖处理函数响应 context 取消，不会强制终止任意 Go 函数。

Implement the task execution engine.

Features: / 功能

- worker pool
- goroutine concurrency
- panic recovery
- task timeout
- retry execution

Status / 状态: `DONE`（已完成）

## Phase 2: Reliability (Milestones 5-8) / 阶段 2：可靠性（里程碑 5–8）

> 中文说明：通过重试、死信存储、Redis 持久化后端和提交去重增强容错能力；不承诺外部副作用恰好执行一次。

Goal: Make task execution reliable and fault-tolerant.

### Milestone 5: Retry Strategy / 里程碑 5：重试策略

> 中文说明：已支持最大尝试次数、初始延迟、延迟上限及退避倍率。工作进程通过递增尝试次数并设置 ScheduledAt 重新提交任务；调用方可覆盖默认策略。验收覆盖退避计算、延迟上限、重试调度及耗尽后停止重试。

Improve retry logic.

Features: / 功能

- exponential backoff
- retry delay scheduling
- configurable retry policies

Example formula: / 公式示例

```text
retry_delay = base * 2^attempt
```

Delivered: / 已交付

- `task.RetryPolicy` with configurable `MaxAttempts`, `InitialDelay`,
  `MaxDelay`, and `Multiplier`
- exponential backoff calculation via `RetryPolicy.NextDelay`
- worker-side retry rescheduling by re-enqueuing the task with a future
  `ScheduledAt` timestamp
- public configuration through `Config.DefaultRetryPolicy` and
  `taskforge.WithRetryPolicy(...)`
- unit coverage for retry delay calculation and retry rescheduling behavior

Acceptance criteria: / 验收标准

- [x] retry delay grows exponentially and respects the configured cap
- [x] retryable failures are re-enqueued with incremented attempt metadata
- [x] callers can override retry policy defaults per task
- [x] retry exhaustion stops re-enqueueing and leaves terminal handling to the
  later reliability milestones

Status / 状态: `DONE`（已完成）

### Milestone 6: Dead Letter Queue / 里程碑 6：死信队列

> 中文说明：已支持内存与 Redis 死信存储。任务耗尽重试后保留 FAILED 结果，并保存原始任务、最终错误、尝试信息和失败时间，供检查与重放。重放使用新任务 ID，保留原记录；代码也已支持重放元数据及单条清除；批量清除仍待实现。验收重点是最终失败可查询，而成功及仍可重试任务不会进入死信队列；这里的记录验收不等同于端到端恰好一次保证。

Handle permanently failed tasks.

Features: / 功能

- max retry limit
- DLQ storage
- DLQ inspection
- DLQ replay

Recommended order of action: / 建议实施顺序

1. persist terminally failed task envelopes in a Redis DLQ keyspace
2. include final failure metadata needed for inspection and replay decisions
3. add library APIs to list and fetch DLQ entries across app instances
4. add CLI inspection support for Redis-backed DLQ state
5. add tests covering retry exhaustion, DLQ persistence, and cross-process reads

Why this comes next: / 实施背景

- persistence is now in place, so failure state can be shared across processes
- retries already exist, so DLQ closes the reliability loop with immediate
  operator value
- it creates a clean foundation for later idempotency and observability work

Delivered: / 已交付

- dedicated DLQ storage boundary with memory and Redis backends
- terminal-failure persistence from the worker on retry exhaustion
- library APIs to fetch, list, and replay DLQ entries
- CLI support for `dlq list`, `dlq get`, and `dlq replay`
- unit and Redis integration coverage for DLQ persistence, inspection, and replay

Proposed scope split: / 建议范围拆分

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

Acceptance criteria: / 验收标准

- [x] a task that exhausts retries is stored under its task ID in the DLQ
- [x] the DLQ entry contains enough metadata to explain why retries stopped
- [x] a separate App instance can list and inspect the DLQ entry through Redis
- [x] successful tasks and still-retrying tasks never appear in the DLQ
- [x] the existing `result` path still reports terminal state for the task ID

Notes: / 备注

- replay currently re-enqueues the dead-lettered task under a fresh task ID
- the original DLQ record is retained for audit and inspection
- replay count, last replay task ID/time, and single-entry purge are implemented
- bulk purge and richer replay-resolution workflows remain deferred

Status / 状态: `DONE`（已完成）

### Milestone 7: Persistence Layer ⭐ / 里程碑 7：持久化层 ⭐

> 中文说明：已交付 Redis 路径，包括共享结果、就绪与延迟队列、执行中预留、确认及租约过期恢复，默认仍使用内存后端。PostgreSQL 和 NATS 属于候选方案，尚未实现。Redis 数据能否抵御服务重启取决于其持久化配置；现有集成测试使用连接同一 Redis 的独立 App 实例验证共享状态。

Move from in-memory to persistent broker.

Backend status / 后端状态:

- Redis: implemented / 已实现
- PostgreSQL: deferred / 待实现
- NATS: optional future backend / 可选的后续后端

Architecture: / 架构

```text
API -> Broker -> Worker -> Result Store
```

Delivered: / 已交付

- Redis-backed broker with ready queues, delayed queues, in-flight reservation,
  `Ack`, and lease-expiry recovery
- Redis-backed result backend with shared cross-process result reads/writes
- backend-selection/config plumbing while keeping in-memory as the default path
- CLI flags for backend selection and Redis connection settings
- integration coverage using separate app instances against a live Redis server
- reproducible local Redis setup via `compose.yml`, `Makefile`, and README docs

Acceptance criteria status: / 验收状态

- [x] a task enqueued by one App instance can be consumed by another worker App
- [x] a result App can read task state written by a separate worker App
- [x] delayed tasks survive worker restarts
- [x] the in-memory implementations still pass the existing unit tests
- [x] failed tasks retain enough metadata to support DLQ inspection next

Follow-on work: / 后续工作

- DLQ replay metadata and single-entry purge are implemented; bulk purge remains deferred
- PostgreSQL and NATS remain deferred
- observability and health endpoints remain later-phase work

Status / 状态: `DONE`（已完成）

### Milestone 8: Idempotency / 里程碑 8：幂等性

> 中文说明：已实现可选的提交键去重：同键返回原任务 ID，无键维持每次新建任务的行为。内存和 Redis 均支持原子认领或复用，入队失败时尝试按所有权回滚认领。终态失败不自动释放键；死信重放清除原键并使用新身份。当前同键即使负载不同也复用，没有键过期、额外作用域或执行锁。认领与入队仍是分开的操作，崩溃窗口的增强方案见后续设计文档。

Prevent duplicate logical tasks from being admitted more than once across
processes.

Problem / 问题背景

Now that Taskforge supports Redis-backed cross-process execution, duplicate
enqueue requests can create multiple task records and multiple broker messages
for what is logically the same job. Milestone 8 adds enqueue-key reuse; claiming the key and publishing the message
remain separate operations, so crash-atomic admission is not yet guaranteed.

Scope / 范围

Add optional enqueue-time idempotency keyed by a caller-supplied string.

Enqueue idempotency is implemented. The [bilingual follow-on design](docs/IDEMPOTENCY.md)
proposes stronger atomic publication, conflict detection, retention and worker
ownership guarantees beyond this milestone's delivered scope.

提交幂等已实现。[双语后续方案](docs/IDEMPOTENCY.md)讨论原子发布、冲突检测、
保留策略与工作进程所有权，属于当前里程碑之外的增强规划。

Features: / 功能

- idempotency key on enqueue
- atomic claim-or-reuse behavior
- shared Redis-backed idempotency store
- in-memory implementation for tests and local parity
- duplicate enqueue returns the canonical existing task ID
- rollback of idempotency claim if enqueue fails
- defined replay behavior for DLQ interaction

Non-goals / 非目标

> 中文说明：不包含通用分布式锁、工作进程恰好一次执行、键 TTL、多作用域、手动释放接口或其他数据库实现。

Do not include:

- generic distributed task locking
- worker-side exactly-once execution guarantees
- automatic reuse expiry or key TTL policies
- idempotency scopes or partitions beyond a single key string
- operator APIs for manual key release
- PostgreSQL or NATS implementations

Behavior / 行为约定

If `Enqueue` is called without an idempotency key:

- preserve current behavior

If `Enqueue` is called with an idempotency key:

- first caller atomically claims the key and creates a new task
- later callers with the same key do not enqueue a second task
- later callers receive the original task ID

Terminal failures do not free the key automatically.
DLQ replay must use a fresh task identity and must not be blocked by the
original key.

API / 公开接口

Add:

- `taskforge.WithIdempotencyKey(key string)`

Extend the internal task message model to carry:

- `IdempotencyKey string`

Implementation / 实现方案

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

Acceptance criteria / 验收标准

> 中文说明：验收覆盖同键复用、并发提交、入队失败回滚、失败后复用、无键行为和死信重放。当前 Redis 测试使用同一进程中的多个 App 实例，独立操作系统进程与崩溃恢复测试仍可增强。

- [x] two enqueue calls with the same idempotency key return the same task ID
- [x] only one broker message is created for a given idempotency key
- [x] concurrent enqueue attempts from separate App instances reuse one ID with Redis
- [x] a definite broker rejection releases the claim when owner-checked rollback succeeds
- [x] duplicate enqueue after terminal task failure still returns the original task ID
- [x] enqueue calls without idempotency keys preserve current behavior
- [x] DLQ replay remains usable and is not blocked by the original task's idempotency key
- [x] unit tests cover in-memory behavior and race cases
- [x] Redis integration tests cover duplicate enqueue across App instances

Status / 状态: `DONE`（已完成）

## Phase 3: Observability (Milestones 9-12) / 阶段 3：可观测性（里程碑 9–12）

> 中文说明：计划补齐结构化日志、指标、分布式追踪和健康检查，使任务执行和系统状态便于观察与排障。

Goal: Provide production-grade monitoring.

### Milestone 9: Structured Logging / 里程碑 9：结构化日志

> 中文说明：计划用 JSON 格式记录请求上下文、任务执行及工作进程事件，可选 slog 或 zap。当前普通日志不等同于本里程碑的结构化日志能力。

Features: / 功能

- JSON logs
- request context logging
- task execution logs

Libraries: / 候选库

- `slog`
- `zap`

Status / 状态: `TODO`（待完成）

### Milestone 10: Metrics / 里程碑 10：指标

> 中文说明：计划采集队列长度、任务延迟、工作池利用率和任务失败等指标，并使用 Prometheus 与 Grafana 进行监控展示。

Integrate monitoring.

Metrics: / 指标

- queue size
- job latency
- worker utilization
- task failures

Stack: / 技术栈

- Prometheus
- Grafana

Status / 状态: `TODO`（待完成）

### Milestone 11: Distributed Tracing / 里程碑 11：分布式追踪

> 中文说明：计划通过 OpenTelemetry 关联从 API、队列、工作进程到结果存储的执行链路，支持跨服务排障。

Trace task execution across services.

Stack: / 技术栈

- OpenTelemetry

Flow: / 流程

```text
API -> Queue -> Worker -> Result
```

Status / 状态: `TODO`（待完成）

### Milestone 12: Health Checks / 里程碑 12：健康检查

> 中文说明：计划提供 /health 和 /ready 接口，供 Kubernetes 探针等基础设施判断服务健康和就绪状态。

Expose service health endpoints.

Endpoints: / 接口

- `/health`
- `/ready`

Used by: / 使用场景

- Kubernetes probes

Status / 状态: `TODO`（待完成）

## Phase 4: Cloud Native Deployment (Milestones 13-16) / 阶段 4：云原生部署（里程碑 13–16）

> 中文说明：计划从容器镜像和本地服务编排逐步扩展到 Kubernetes 部署及自动扩缩容。

Goal: Deploy Taskforge as a cloud-native system.

### Milestone 13: Containerization / 里程碑 13：容器化

> 中文说明：计划通过 Dockerfile 和多阶段构建生成 Taskforge 容器镜像。

Create container images.

Artifacts: / 交付产物

- `Dockerfile`
- multi-stage build

Status / 状态: `TODO`（待完成）

### Milestone 14: Local Dev Environment / 里程碑 14：本地开发环境

> 中文说明：目标是在本地统一运行 API、Worker、Redis 和 Prometheus。当前已有用于 Redis 的 Compose 配置，完整服务栈尚未交付，因此仍标记 TODO。

Run the stack locally.

Tools: / 工具

- `docker-compose`

Services: / 服务

- api
- worker
- redis
- prometheus

Status / 状态: `TODO`（待完成）

### Milestone 15: Kubernetes Deployment / 里程碑 15：Kubernetes 部署

> 中文说明：计划提供 Deployment、Service、ConfigMap 和 Secret 等部署资源，管理服务运行、访问及配置。

Deploy services to Kubernetes.

Resources: / 资源

- Deployment
- Service
- ConfigMap
- Secret

Status / 状态: `TODO`（待完成）

### Milestone 16: Autoscaling / 里程碑 16：自动扩缩容

> 中文说明：计划使用 HPA 和队列长度等指标，根据负载调整工作进程数量。

Scale workers automatically.

Methods: / 方法

- HPA
- queue length metrics

Status / 状态: `TODO`（待完成）

## Phase 5: Advanced Features (Milestones 17-20) / 阶段 5：高级功能（里程碑 17–20）

> 中文说明：在任务执行基础之上，逐步增加完整调度、限流、任务依赖和可视化管理能力。

Goal: Turn Taskforge into a workflow platform.

### Milestone 17: Scheduled Tasks / 里程碑 17：任务调度

> 中文说明：当前已有单次延迟任务和固定间隔调度；cron 表达式支持尚未实现，因此该里程碑仍标记 TODO。

Features: / 功能

- delayed tasks
- cron scheduling

Status / 状态: `TODO`（待完成）

### Milestone 18: Rate Limiting / 里程碑 18：限流

> 中文说明：计划按队列或租户限制任务处理速率，控制吞吐和资源占用。

Control task throughput.

Features: / 功能

- per-queue rate limit
- per-tenant limit

Status / 状态: `TODO`（待完成）

### Milestone 19: DAG Workflows / 里程碑 19：DAG 工作流

> 中文说明：计划通过有向无环图表达任务依赖，例如任务 B 等待任务 A 完成后再执行，逐步支持工作流编排。

Support task dependencies.

Example: / 示例

```text
Task B depends on Task A
```

Similar to: / 参考系统

- Airflow
- Temporal

Status / 状态: `TODO`（待完成）

### Milestone 20: Web Dashboard / 里程碑 20：Web 管理界面

> 中文说明：计划提供任务状态、工作进程统计和指标视图，方便检查运行情况。

Provide UI for monitoring.

Features: / 功能

- task status
- worker stats
- metrics view

Status / 状态: `TODO`（待完成）
