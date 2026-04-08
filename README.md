# taskforge

Taskforge is a Go prototype for background job execution. It gives you a task
registry, worker pool, delayed jobs, retries, periodic schedules, result
tracking, DLQ support, and enqueue-time idempotency behind a small API.

The project now supports both in-memory and Redis-backed broker, result, DLQ,
and idempotency components. Redis is now the default runtime model for the CLI
and the recommended path for real multi-process usage. The in-memory path
remains available for the demo, tests, and embedded single-process
experimentation.

> Status: prototype / work-in-progress.
> The Redis-backed CLI golden path now works across processes; system semantics,
> observability, and production hardening are still evolving.

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
| CLI runtime | `worker`, `enqueue`, `result`, and `dlq` share Redis state by default |
| Demo CLI | Self-contained in-memory runnable example |

## Getting Started

### Prerequisites

- Go `1.24.13` or newer

### 1. Start local Redis

```bash
docker compose up -d redis
```

### 2. Run the CLI golden path

Start a worker in one terminal:

```bash
go run ./cmd/taskforge worker
```

Enqueue a task from another terminal:

```bash
go run ./cmd/taskforge enqueue -name echo -payload '{"msg":"hello"}'
```

Fetch the result:

```bash
go run ./cmd/taskforge result -id <task-id>
```

The `result` command also accepts a unique task ID prefix:

```bash
go run ./cmd/taskforge result -id <task-id-prefix>
```

The built-in CLI worker registers an `echo` handler so the cross-process flow
works out of the box against Redis.

### 3. Run the demo

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

### 4. Build the CLI

```bash
go build -o bin/taskforge ./cmd/taskforge
./bin/taskforge worker
```

Or install it onto your `PATH`:

```bash
make install
taskforge worker
```

### 4.1 Repo-local zsh completion

If you use `zsh`, you can load completions for `taskforge` in the current shell
without installing anything globally:

```bash
source ./scripts/taskforge-completion.zsh
```

That only affects the current shell session started in this repository. It does
not modify your global `~/.zshrc` or system-wide completion paths.

If you use `direnv`, the repo also includes a local [.envrc](/Users/onelaststop/Workspace/taskforge/.envrc) that prepends `./bin` to `PATH`.

Enable it once:

```bash
direnv allow
```

`direnv` is intentionally not used to modify `FPATH`. That approach is easy to
get wrong in existing zsh setups and can interfere with normal tab completion.
Use the repo-local loader script instead when you want taskforge completion in
the current shell:

```bash
source ./scripts/taskforge-completion.zsh
```

### 5. Run the tests

```bash
go test ./...
```

Then run the Redis integration tests:

```bash
go test ./pkg/taskforge -run RedisIntegration -v
```

Environment overrides:

- `TASKFORGE_REDIS_ADDR` defaults to `127.0.0.1:6379`
- `TASKFORGE_REDIS_USERNAME` defaults to empty
- `TASKFORGE_REDIS_PASSWORD` defaults to empty
- `TASKFORGE_REDIS_DB` defaults to `0` for CLI commands and `15` in Redis integration tests

## CLI Status

The CLI now follows a single default runtime model:

```bash
./bin/taskforge worker
./bin/taskforge enqueue -name echo -payload '{"msg":"hello"}'
./bin/taskforge result -id <task-id-or-prefix>
```

By default, those commands use Redis for broker, result, DLQ, and idempotency
state, so separate processes share the same runtime data.

The `result` command resolves exact task IDs and unique prefixes. If a prefix
matches more than one result ID, the command exits with an ambiguity error.

For isolated local experiments, you can still force in-memory behavior:

```bash
./bin/taskforge worker -broker-backend memory -result-backend memory
./bin/taskforge enqueue -broker-backend memory -result-backend memory -name echo
```

The `demo` command remains the fastest fully self-contained in-memory path.

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
