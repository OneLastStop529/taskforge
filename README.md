# taskforge / Taskforge 项目说明

> 中文说明：Taskforge 是一个用 Go 编写的后台任务执行原型，提供任务注册、并发工作池、延迟执行、重试、周期调度和结果查询等功能。既可嵌入单个进程使用，也可通过 Redis 在多个进程之间共享任务和结果。项目仍处于原型阶段，CLI 和本地开发流程仍在完善；设计参考了 Celery、Temporal 等任务处理系统。

Taskforge is a Go prototype for background job execution. It gives you a task
registry, worker pool, delayed jobs, retries, periodic schedules, result
tracking, DLQ support, and enqueue-time idempotency behind a small API.

The project now supports both in-memory and Redis-backed broker, result, DLQ,
and idempotency components. The in-memory path is still the default for the
demo and most unit tests; Redis is used for cross-process integration tests
and persistent execution flows.

> Status: prototype / work-in-progress.
> Redis-backed multi-process execution now exists behind the backend
> abstractions, but the CLI and local developer workflow are still evolving.

Taskforge is influenced by [Celery](https://docs.celeryq.dev),
[Temporal](https://temporal.io), and similar job-processing systems.

## What Works Today / 已实现的功能

> 中文说明：目前支持按名称注册 JSON 任务、内存和 Redis 队列、带保留期限的结果存储、可配置并发工作池、指数退避重试、固定间隔调度，以及通过 context 传递的任务超时。Redis 队列还支持执行中任务的预留与租约过期恢复。代码中另已实现死信队列（DLQ）的查询、重放和清除功能。

| Feature | Details |
|---|---|
| Task registry | Name-based handlers with JSON payloads |
| In-memory broker | Queueing plus delayed delivery in one process |
| In-memory result backend | Result persistence with TTL expiry |
| Redis broker | Shared ready/delayed queues with in-flight reservation recovery |
| Redis result backend | Shared cross-process task result storage |
| DLQ support | Dead-letter persistence, inspection, replay, and purge |
| Enqueue idempotency | Canonical task reuse across duplicate enqueue requests |
| Worker pool | Configurable concurrency and graceful shutdown |
| Retry policy | Exponential backoff with max-attempt controls |
| Periodic tasks | Interval scheduling via `EverySchedule` |
| Per-task timeout | Handler context deadline support |
| Demo CLI | End-to-end runnable example |

## Getting Started / 快速开始

> 中文说明：建议先运行无需 Redis 的演示，再根据需要构建 CLI、运行测试或启动 Redis 集成环境。

### Prerequisites / 环境要求

> 中文说明：需要 Go 1.24.13 或更高版本。运行下方 Docker Compose 命令还需要可用的 Docker 环境。

- Go `1.24.13` or newer
- Docker with Compose and a running Docker daemon (step 4 only)

Run the following commands from the repository root, where `go.mod` is located.

请在仓库根目录（包含 `go.mod` 的目录）执行以下命令。仅第 4 步需要 Docker、Compose 和已启动的 Docker 服务。

### 1. Run the demo / 1. 运行演示

> 中文说明：这是验证项目能否构建、核心流程能否运行的最快方式。演示会注册任务处理函数，启动工作池和周期调度器，提交若干任务并打印结果；这些组件共享同一进程的内存状态。

This is the fastest way to confirm the project builds and the core flow works.

```bash
go run ./cmd/taskforge demo
```

What the demo does:

- registers a couple of task handlers
- starts the worker pool
- starts the interval scheduler
- enqueues several jobs
- prints their results

### 2. Build the CLI / 2. 构建命令行工具

> 中文说明：以下命令将可执行文件输出到 bin/taskforge，然后运行演示。

Go creates the output directory if needed. Run the resulting CLI with the
`demo` subcommand to execute the self-contained example.

Go 会在需要时自动创建输出目录；构建后使用 `demo` 子命令运行完整演示。

```bash
go build -o bin/taskforge ./cmd/taskforge
./bin/taskforge demo
```

### 3. Run the tests / 3. 运行测试

> 中文说明：以下命令运行各 Go 包中的测试；实际连接 Redis 的集成测试需要可用的 Redis 服务。

```bash
go test ./...
```

Redis integration tests are skipped if Redis is unavailable; a passing command
does not by itself confirm Redis integration coverage. See step 4 to run them.

Redis 不可用时，集成测试会跳过；命令通过并不代表 Redis 集成测试已实际执行。请按第 4 步配置后验证。

### 4. Start local Redis for integration work / 4. 启动本地 Redis 集成环境

> 中文说明：先启动 Redis，再运行名称匹配 RedisIntegration 的测试。可用 TASKFORGE_REDIS_ADDR 覆盖默认地址 127.0.0.1:6379，用 TASKFORGE_REDIS_DB 覆盖测试使用的默认数据库 15。

```bash
docker compose up -d redis
```

Wait for Redis to report `PONG` before running the integration tests:

运行集成测试前，先确认 Redis 已就绪，以下命令应返回 `PONG`：

```bash
docker compose exec redis redis-cli ping
```

The tests clear the selected Redis database before and after each test. Use a
dedicated test instance/database with no data you need to keep (database `15`
by default).

测试会在每项测试前后清空所选 Redis 数据库。请使用不含需保留数据的专用测试实例或数据库（默认数据库为 `15`）。

Then run the Redis integration tests and verify they report `PASS`, not `SKIP`:

然后运行 Redis 集成测试，确认各项结果为 `PASS`，而非 `SKIP`：

```bash
go test ./pkg/taskforge -run RedisIntegration -count=1 -v
```

Environment overrides:

- `TASKFORGE_REDIS_ADDR` defaults to `127.0.0.1:6379`
- `TASKFORGE_REDIS_DB` defaults to `15`

## CLI Status / 命令行工具现状

> 中文说明：demo 是最方便的独立演示入口。worker、enqueue 和 result 支持选择后端，但默认各自创建独立的内存状态：分别启动这些命令时，工作进程无法读取另一个进程提交的任务，查询进程也无法读取其他进程的结果。跨进程使用时，请将相关后端配置为 Redis，并使用一致的连接设置。

The `demo` command is still the fastest fully self-contained path.

The CLI now accepts backend-selection flags for `worker`, `enqueue`, `result`,
and `dlq`. If you leave the defaults in place, each invocation creates its own
isolated in-memory state. Those processes do not communicate across process
boundaries:

```bash
./bin/taskforge worker
./bin/taskforge enqueue -name echo -payload '{"msg":"hello"}'
./bin/taskforge result -id <task-id>
```

That means, with default memory settings:

- `worker` cannot consume tasks created by a separate `enqueue` process
- `result` cannot see results created by a separate worker process
- `dlq` inspection only sees entries created in the same process
- duplicate enqueue protection is only shared across processes when the
  idempotency backend is also configured to use Redis
- for real end-to-end shared-state behavior, configure Redis-backed broker,
  result, DLQ, and idempotency backends

## Using It As a Library / 作为 Go 库使用

> 中文说明：进程内使用时，让应用、工作池与结果存储共享同一个 App。下面的示例注册 add 任务，将参数序列化为 JSON，启动工作池，提交任务并读取结果。示例中的短暂等待仅用于演示，并不保证任意任务都能在这段时间内完成。

For in-process experimentation, embed Taskforge directly so the app, worker,
and in-memory backends share the same local state.

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/OneLastStop529/taskforge/pkg/taskforge"
)

func main() {
	app := taskforge.New(taskforge.DefaultConfig())
	defer app.Close()

	app.Register("add", func(ctx context.Context, payload []byte) ([]byte, error) {
		var args struct {
			A int
			B int
		}
		if err := json.Unmarshal(payload, &args); err != nil {
			return nil, err
		}
		return json.Marshal(args.A + args.B)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		_ = app.StartWorker(ctx)
	}()

	id, err := app.Enqueue(ctx, "add", map[string]int{"A": 3, "B": 7})
	if err != nil {
		panic(err)
	}

	time.Sleep(100 * time.Millisecond)

	result, err := app.GetResult(ctx, id)
	if err != nil {
		panic(err)
	}

	fmt.Printf("state=%s output=%s\n", result.State, result.Output)
}
```

Expected output:

```text
state=SUCCESS output=10
```

## Common API Surface / 常用 API

> 中文说明：通过 App 注册和提交任务、读取结果、启动工作池与调度器；提交选项用于控制单个任务的行为。

### Enqueue options / 任务提交选项

> 中文说明：下面展示指定队列、延迟十秒执行，以及设置三十秒任务超时。超时通过 context 传给处理函数，需要处理函数配合响应取消信号；消费任务的工作池也需要监听对应队列。

```go
app.Enqueue(ctx, "task", payload,
	taskforge.WithQueue("high-priority"),
	taskforge.WithDelay(10*time.Second),
	taskforge.WithTimeout(30*time.Second),
	taskforge.WithIdempotencyKey("invoice:123"),
)
```

### Idempotent enqueue / 幂等提交

> 中文说明：WithIdempotencyKey 已实现。同一键重复提交返回原任务 ID，即使负载不同也复用；当前没有键过期或额外作用域。

```go
id, err := app.Enqueue(ctx, "charge_customer", payload,
	taskforge.WithIdempotencyKey("invoice:123"),
)
```

Later enqueue calls with the same idempotency key return the same canonical
task ID and do not admit duplicate work.

### Periodic tasks / 周期任务

```go
app.AddSchedule("heartbeat", "ping", "default", 1*time.Minute, nil)
go app.StartScheduler(ctx)
```

## Project Layout / 项目结构

> 中文说明：cmd/taskforge 是 CLI 入口，pkg/taskforge 提供公开 API；internal 下按任务模型、队列、工作池、调度和结果存储划分职责。除下方目录外，internal/dlq 提供死信存储，internal/redis 封装 Redis 连接设置；broker 和 result 均已有内存与 Redis 实现。

```text
cmd/taskforge/       CLI entry point
internal/broker/     Broker interface + in-memory implementation
internal/dlq/        Dead-letter queue backends
internal/idempotency/ Enqueue idempotency backends
internal/result/     Result backend interface + in-memory implementation
internal/scheduler/  Periodic task scheduler
internal/task/       Message types, retry policy, handler registry
internal/worker/     Worker pool and execution loop
pkg/taskforge/       Public API
```

## Additional Docs / 更多文档

- [Workflow Diagram](./WORKFLOW.md)
- [Architecture](./ARCHITECTURE.md)
- [Milestones](./MILESTONES.md)

## Roadmap / 后续计划

- [x] Redis broker implementation
- [x] Redis result backend implementation
- [ ] Cron expression support in the scheduler
- [ ] Observability: structured logging, metrics, tracing
