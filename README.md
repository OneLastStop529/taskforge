# taskforge
A cloud-native distributed task processing platform written in Go.

Taskforge is inspired by Celery, Temporal, and distributed job processing systems.
It provides an ergonomic API for defining, enqueuing, and executing tasks across a
pool of concurrent workers, with built-in retry logic, scheduled tasks, and a
swappable broker / result-backend architecture.

---

## Features

| Feature | Details |
|---|---|
| **Task registry** | Name-keyed handler functions with type-safe JSON payloads |
| **In-memory broker** | Zero-dependency, channel-backed queue with scheduled delivery |
| **In-memory result backend** | TTL-aware result store with automatic expiry |
| **Worker pool** | Configurable concurrency; graceful shutdown |
| **Retry with backoff** | Per-task `RetryPolicy` with exponential backoff and a configurable cap |
| **Periodic tasks** | Celery-Beat-style scheduler with `EverySchedule` |
| **Per-task timeout** | Context-based deadline propagated to each handler |
| **CLI** | `worker`, `enqueue`, `result`, and `demo` sub-commands |

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

## Quick start

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

```
# Run the in-process demo
taskforge demo

# Start a worker (blocks until SIGINT/SIGTERM)
taskforge worker -concurrency 8 -queue default

# Enqueue a task from the command line
taskforge enqueue -name echo -payload '{"msg":"hello"}' -delay 5s

# Retrieve a result
taskforge result -id <task-id>
```

---

## Running tests

```
go test ./...
```
