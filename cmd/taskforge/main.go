// Command taskforge is the CLI entry point for the Taskforge platform.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/OneLastStop529/taskforge/pkg/taskforge"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "worker":
		runWorker(os.Args[2:])
	case "enqueue":
		runEnqueue(os.Args[2:])
	case "result":
		runResult(os.Args[2:])
	case "demo":
		runDemo()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `
Taskforge – in-process task execution platform (prototype)

Usage:
  taskforge <command> [flags]

Commands:
  demo      Run a self-contained in-process demonstration (recommended)
  worker    [scaffolding] Start an in-process worker (ephemeral, not cross-process)
  enqueue   [scaffolding] Enqueue a task into a private in-process broker
  result    [scaffolding] Retrieve a result from a private in-process store

NOTE: worker, enqueue, and result each create their own isolated in-memory
      broker and result backend. They do not share state across processes.
      Use 'demo' for a fully functional end-to-end example.
`)
}

func runWorker(args []string) {
	fs := flag.NewFlagSet("worker", flag.ExitOnError)
	concurrency := fs.Int("concurrency", 10, "number of concurrent workers")
	queue := fs.String("queue", "default", "queue name to consume from")
	brokerBackend := fs.String("broker-backend", string(taskforge.BackendMemory), "broker backend: memory or redis")
	resultBackend := fs.String("result-backend", string(taskforge.BackendMemory), "result backend: memory or redis")
	redisAddr := fs.String("redis-addr", "127.0.0.1:6379", "Redis address")
	redisUsername := fs.String("redis-username", "", "Redis username")
	redisPassword := fs.String("redis-password", "", "Redis password")
	redisDB := fs.Int("redis-db", 0, "Redis database index")
	_ = fs.Parse(args)

	cfg := cliConfig()
	cfg.Concurrency = *concurrency
	cfg.DefaultQueue = *queue
	cfg.BrokerBackend = taskforge.BackendKind(*brokerBackend)
	cfg.ResultBackend = taskforge.BackendKind(*resultBackend)
	cfg.Redis = taskforge.RedisConfig{
		Addr:     *redisAddr,
		Username: *redisUsername,
		Password: *redisPassword,
		DB:       *redisDB,
	}

	app, err := taskforge.Open(cfg)
	if err != nil {
		log.Fatalf("taskforge worker: %v", err)
	}
	defer app.Close() //nolint:errcheck
	// Register a simple echo handler for demonstration.
	app.Register("echo", func(_ context.Context, payload []byte) ([]byte, error) {
		return payload, nil
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Printf("taskforge worker: starting (concurrency=%d queue=%s)", *concurrency, *queue)
	if cfg.BrokerBackend == taskforge.BackendMemory && cfg.ResultBackend == taskforge.BackendMemory {
		log.Printf("NOTE: using in-memory broker/result backend — state is not shared with other processes; use 'demo' for a fully functional example")
	}
	if err := app.StartWorker(ctx); err != nil {
		log.Fatalf("taskforge worker: %v", err)
	}
}

func runEnqueue(args []string) {
	fs := flag.NewFlagSet("enqueue", flag.ExitOnError)
	name := fs.String("name", "", "task name (required)")
	payload := fs.String("payload", "{}", "JSON payload")
	queue := fs.String("queue", "default", "destination queue")
	delay := fs.Duration("delay", 0, "delay before task runs (e.g. 5s)")
	brokerBackend := fs.String("broker-backend", string(taskforge.BackendMemory), "broker backend: memory or redis")
	resultBackend := fs.String("result-backend", string(taskforge.BackendMemory), "result backend: memory or redis")
	redisAddr := fs.String("redis-addr", "127.0.0.1:6379", "Redis address")
	redisUsername := fs.String("redis-username", "", "Redis username")
	redisPassword := fs.String("redis-password", "", "Redis password")
	redisDB := fs.Int("redis-db", 0, "Redis database index")
	_ = fs.Parse(args)

	if *name == "" {
		fmt.Fprintln(os.Stderr, "enqueue: -name is required")
		os.Exit(1)
	}

	cfg := cliConfig()
	cfg.DefaultQueue = *queue
	cfg.BrokerBackend = taskforge.BackendKind(*brokerBackend)
	cfg.ResultBackend = taskforge.BackendKind(*resultBackend)
	cfg.Redis = taskforge.RedisConfig{
		Addr:     *redisAddr,
		Username: *redisUsername,
		Password: *redisPassword,
		DB:       *redisDB,
	}
	if cfg.BrokerBackend == taskforge.BackendMemory && cfg.ResultBackend == taskforge.BackendMemory {
		log.Printf("NOTE: enqueue uses an ephemeral in-memory broker/result backend; the task ID printed below is not visible to any other process")
	}
	app, err := taskforge.Open(cfg)
	if err != nil {
		log.Fatalf("enqueue: %v", err)
	}
	defer app.Close() //nolint:errcheck

	ctx := context.Background()
	var opts []taskforge.EnqueueOption
	if *delay > 0 {
		opts = append(opts, taskforge.WithDelay(*delay))
	}
	id, err := app.Enqueue(ctx, *name, json.RawMessage(*payload), opts...)
	if err != nil {
		log.Fatalf("enqueue: %v", err)
	}
	fmt.Println(id)
}

func runResult(args []string) {
	fs := flag.NewFlagSet("result", flag.ExitOnError)
	id := fs.String("id", "", "task ID (required)")
	brokerBackend := fs.String("broker-backend", string(taskforge.BackendMemory), "broker backend: memory or redis")
	resultBackend := fs.String("result-backend", string(taskforge.BackendMemory), "result backend: memory or redis")
	redisAddr := fs.String("redis-addr", "127.0.0.1:6379", "Redis address")
	redisUsername := fs.String("redis-username", "", "Redis username")
	redisPassword := fs.String("redis-password", "", "Redis password")
	redisDB := fs.Int("redis-db", 0, "Redis database index")
	_ = fs.Parse(args)

	if *id == "" {
		fmt.Fprintln(os.Stderr, "result: -id is required")
		os.Exit(1)
	}

	cfg := cliConfig()
	cfg.BrokerBackend = taskforge.BackendKind(*brokerBackend)
	cfg.ResultBackend = taskforge.BackendKind(*resultBackend)
	cfg.Redis = taskforge.RedisConfig{
		Addr:     *redisAddr,
		Username: *redisUsername,
		Password: *redisPassword,
		DB:       *redisDB,
	}
	if cfg.BrokerBackend == taskforge.BackendMemory && cfg.ResultBackend == taskforge.BackendMemory {
		log.Printf("NOTE: result uses an ephemeral in-memory store; it will always return 'not found' for IDs produced by other processes")
	}
	app, err := taskforge.Open(cfg)
	if err != nil {
		log.Fatalf("result: %v", err)
	}
	defer app.Close() //nolint:errcheck

	ctx := context.Background()
	r, err := app.GetResult(ctx, *id)
	if err != nil {
		log.Fatalf("result: %v", err)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(r)
}

func runDemo() {
	cfg := cliConfig()
	cfg.Concurrency = 4
	app := taskforge.NewMemory(cfg)
	defer app.Close() //nolint:errcheck

	// Register a few tasks.
	app.Register("add", func(_ context.Context, payload []byte) ([]byte, error) {
		var args struct{ A, B int }
		if err := json.Unmarshal(payload, &args); err != nil {
			return nil, err
		}
		sum := args.A + args.B
		log.Printf("add: %d + %d = %d", args.A, args.B, sum)
		return json.Marshal(map[string]int{"result": sum})
	})

	app.Register("sleep", func(_ context.Context, payload []byte) ([]byte, error) {
		var args struct{ Ms int }
		_ = json.Unmarshal(payload, &args)
		time.Sleep(time.Duration(args.Ms) * time.Millisecond)
		log.Printf("sleep: slept %dms", args.Ms)
		return nil, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Start the worker in the background.
	go func() {
		if err := app.StartWorker(ctx); err != nil && ctx.Err() == nil {
			log.Printf("worker stopped: %v", err)
		}
	}()

	// Add a periodic task (fires every 2s in the demo).
	_ = app.AddSchedule("heartbeat", "sleep", "default", 2*time.Second, map[string]int{"Ms": 50})
	go app.StartScheduler(ctx)

	// Enqueue a few tasks.
	ids := make([]string, 0, 5)
	for i := 0; i < 5; i++ {
		id, err := app.Enqueue(ctx, "add", map[string]int{"A": i, "B": i * 2})
		if err != nil {
			log.Printf("enqueue error: %v", err)
			continue
		}
		ids = append(ids, id)
	}

	// Wait for all tasks to complete.
	time.Sleep(2 * time.Second)

	fmt.Println("\n--- Results ---")
	for _, id := range ids {
		r, err := app.GetResult(ctx, id)
		if err != nil {
			fmt.Printf("  %s: error: %v\n", id, err)
			continue
		}
		fmt.Printf("  %s [%s]: %s\n", id, r.State, string(r.Output))
	}
	fmt.Println()
}

func cliConfig() taskforge.Config {
	return taskforge.DefaultConfig()
}
