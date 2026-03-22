# taskforge

Taskforge is a Go prototype for in-process background job execution. It gives
you a task registry, worker pool, delayed jobs, retries, periodic schedules,
and result tracking behind a small API.

The current implementation is intentionally simple: broker state and task
results live in memory inside a single process. When the process exits, all
state is lost.

> Status: prototype / work-in-progress.
>
> The extension points are already in place (`broker.Broker`,
> `result.Backend`), but only the in-memory implementations exist today.
> Multi-process workflows are scaffolded, not finished.

Taskforge is influenced by [Celery](https://docs.celeryq.dev),
[Temporal](https://temporal.io), and similar job-processing systems.

## What Works Today

| Feature | Details |
|---|---|
| Task registry | Name-based handlers with JSON payloads |
| In-memory broker | Queueing plus delayed delivery in one process |
| In-memory result backend | Result persistence with TTL expiry |
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

## The Important Constraint

The `demo` command is the only fully useful CLI path right now.

The other commands exist as scaffolding for a future external broker/result
backend. Each invocation creates its own isolated in-memory state, so these do
not communicate across separate processes:

```bash
./bin/taskforge worker
./bin/taskforge enqueue -name echo -payload '{"msg":"hello"}'
./bin/taskforge result -id <task-id>
```

That means:

- `worker` cannot consume tasks created by a separate `enqueue` process
- `result` cannot see results created by a separate worker process
- for real end-to-end behavior today, use `demo` or embed the library in one process

## Using It As a Library

For actual experimentation, embed Taskforge directly so the app, worker, and
result backend share the same in-memory state.

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
)
```

### Periodic tasks

```go
app.AddSchedule("heartbeat", "ping", "default", 1*time.Minute, nil)
go app.StartScheduler(ctx)
```

## Project Layout

```text
cmd/taskforge/       CLI entry point
internal/broker/     Broker interface + in-memory implementation
internal/result/     Result backend interface + in-memory implementation
internal/scheduler/  Periodic task scheduler
internal/task/       Message types, retry policy, handler registry
internal/worker/     Worker pool and execution loop
pkg/taskforge/       Public API
```

## Roadmap

- [ ] Redis broker implementation
- [ ] Redis result backend implementation
- [ ] Cron expression support in the scheduler
- [ ] Observability: structured logging, metrics, tracing
