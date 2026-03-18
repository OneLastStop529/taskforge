# taskforge
An in-process task execution platform written in Go — a prototype scaffold
designed to evolve into a cloud-native distributed system.

> **Status: prototype / work-in-progress.**  
> Both the broker and the result backend are currently in-memory only.
> All state is private to a single process and is lost when it exits.
> The package interfaces (`broker.Broker`, `result.Backend`) are designed
> so that external implementations (Redis, AMQP, …) can be swapped in
> without changing application code.  
> Taskforge is inspired by [Celery](https://docs.celeryq.dev),
> [Temporal](https://temporal.io), and distributed job processing systems.

---

## Current capabilities

| Feature | Details |
|---|---|
| **Task registry** | Name-keyed handler functions with type-safe JSON payloads |
| **In-memory broker** | Channel-backed queue with scheduled / delayed delivery (single-process) |
| **In-memory result backend** | TTL-aware result store with automatic expiry (single-process) |
| **Worker pool** | Configurable concurrency; graceful shutdown |
| **Retry with backoff** | Per-task `RetryPolicy` with exponential backoff and a configurable cap |
| **Periodic tasks** | Celery-Beat-style scheduler with `EverySchedule` |
| **Per-task timeout** | Context-based deadline propagated to each handler |
| **CLI `demo`** | Self-contained, end-to-end runnable demonstration |

---

## Project layout

```
cmd/taskforge/       – CLI entry point
internal/
  broker/            – Broker interface + in-memory implementation
  result/            – Result-backend interface + in-memory implementation
  scheduler/         – Periodic task scheduler
  task/              – Core message types, retry policy, handler registry
  worker/            – Worker pool and task execution loop
pkg/taskforge/       – Public API (App, EnqueueOption helpers, …)
```

---

## Quick start (single process)

The simplest way to try Taskforge is to embed the App directly — worker,
enqueuer, and result store all share the same in-memory broker within one
process:

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

    // Register a task handler.
    app.Register("add", func(ctx context.Context, payload []byte) ([]byte, error) {
        var args struct{ A, B int }
        _ = json.Unmarshal(payload, &args)
        return json.Marshal(args.A + args.B)
    })

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    // Start the worker pool in the background.
    go app.StartWorker(ctx)

    // Enqueue a task and retrieve its result.
    id, _ := app.Enqueue(ctx, "add", map[string]int{"A": 3, "B": 7})
    time.Sleep(100 * time.Millisecond)

    result, _ := app.GetResult(ctx, id)
    fmt.Printf("state=%s output=%s\n", result.State, result.Output)
    // state=SUCCESS output=10
}
```

### Enqueue options

```go
app.Enqueue(ctx, "task", payload,
    taskforge.WithQueue("high-priority"),        // custom queue
    taskforge.WithDelay(10*time.Second),         // delayed execution
    taskforge.WithTimeout(30*time.Second),       // per-task timeout
    taskforge.WithRetryPolicy(task.RetryPolicy{  // custom retry policy
        MaxAttempts:  5,
        InitialDelay: 500 * time.Millisecond,
        MaxDelay:     60 * time.Second,
        Multiplier:   2.0,
    }),
)
```

### Periodic tasks

```go
app.AddSchedule("heartbeat", "ping", "default", 1*time.Minute, nil)
go app.StartScheduler(ctx)
```

---

## CLI

### `demo` — recommended entry point

```
taskforge demo
```

Runs a fully self-contained, in-process demonstration: registers tasks,
starts a worker pool and a periodic scheduler, enqueues five jobs, and
prints the results.

### Other sub-commands (scaffolding only)

> **⚠ These sub-commands are scaffolding for future multi-process use.**
> Each invocation creates its own ephemeral in-memory broker and result
> backend, so `worker`, `enqueue`, and `result` **do not share state**
> across separate processes. They will become useful once an external
> broker (e.g. Redis) is wired in.

```
# Start a worker (in-process, ephemeral state — not useful cross-process yet)
taskforge worker -concurrency 8 -queue default

# Enqueue a task (writes to a private in-memory broker — not visible to other processes)
taskforge enqueue -name echo -payload '{"msg":"hello"}' -delay 5s

# Retrieve a result (reads from a private in-memory store — not shared with other processes)
taskforge result -id <task-id>
```

---

## Running tests

```
go test ./...
```

---

## Roadmap

The broker and result-backend interfaces are already defined; the next
natural steps are:

- [ ] Redis broker implementation
- [ ] Redis result backend implementation
- [ ] Cron expression support in the scheduler
- [ ] Observability: structured logging, metrics, tracing
