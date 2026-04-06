# taskforge

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

## What Works Today

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

## Getting Started

### Prerequisites

- Go `1.24.13` or newer

### 1. Run the demo

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

### 2. Build the CLI

```bash
go build -o bin/taskforge ./cmd/taskforge
./bin/taskforge demo
```

### 3. Run the tests

```bash
go test ./...
```

### 4. Start local Redis for integration work

```bash
docker compose up -d redis
```

Then run the Redis integration tests:

```bash
go test ./pkg/taskforge -run RedisIntegration -v
```

Environment overrides:

- `TASKFORGE_REDIS_ADDR` defaults to `127.0.0.1:6379`
- `TASKFORGE_REDIS_DB` defaults to `15`

## CLI Status

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

## Using It As a Library

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

## Common API Surface

### Enqueue options

```go
app.Enqueue(ctx, "task", payload,
	taskforge.WithQueue("high-priority"),
	taskforge.WithDelay(10*time.Second),
	taskforge.WithTimeout(30*time.Second),
	taskforge.WithIdempotencyKey("invoice:123"),
)
```

### Idempotent enqueue

```go
id, err := app.Enqueue(ctx, "charge_customer", payload,
	taskforge.WithIdempotencyKey("invoice:123"),
)
```

Later enqueue calls with the same idempotency key return the same canonical
task ID and do not admit duplicate work.

### Periodic tasks

```go
app.AddSchedule("heartbeat", "ping", "default", 1*time.Minute, nil)
go app.StartScheduler(ctx)
```

## Project Layout

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

## Additional Docs

- [Workflow Diagram](./WORKFLOW.md)
- [Architecture](./ARCHITECTURE.md)
- [Milestones](./MILESTONES.md)

## Roadmap

- [x] Redis broker implementation
- [x] Redis result backend implementation
- [ ] Cron expression support in the scheduler
- [ ] Observability: structured logging, metrics, tracing
