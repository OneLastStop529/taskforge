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
	case "dlq":
		runDLQ(os.Args[2:])
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
  dlq       Inspect dead-lettered tasks

NOTE: worker, enqueue, and result each create their own isolated in-memory
      broker and result backend. They do not share state across processes.
      Use 'demo' for a fully functional end-to-end example.
`)
}

type backendFlags struct {
	brokerBackend *string
	resultBackend *string
	dlqBackend    *string
	redisAddr     *string
	redisUsername *string
	redisPassword *string
	redisDB       *int
}

func bindBackendFlags(fs *flag.FlagSet, includeBroker bool) backendFlags {
	flags := backendFlags{}
	if includeBroker {
		flags.brokerBackend = fs.String("broker-backend", string(taskforge.BackendMemory), "broker backend: memory or redis")
	}
	flags.resultBackend = fs.String("result-backend", string(taskforge.BackendMemory), "result backend: memory or redis")
	flags.dlqBackend = fs.String("dlq-backend", "", "dlq backend: memory or redis (defaults to result backend)")
	flags.redisAddr = fs.String("redis-addr", "127.0.0.1:6379", "Redis address")
	flags.redisUsername = fs.String("redis-username", "", "Redis username")
	flags.redisPassword = fs.String("redis-password", "", "Redis password")
	flags.redisDB = fs.Int("redis-db", 0, "Redis database index")
	return flags
}

func (f backendFlags) apply(cfg taskforge.Config) taskforge.Config {
	if f.brokerBackend != nil {
		cfg.BrokerBackend = taskforge.BackendKind(*f.brokerBackend)
	}
	if f.resultBackend != nil {
		cfg.ResultBackend = taskforge.BackendKind(*f.resultBackend)
	}
	if f.dlqBackend != nil {
		cfg.DLQBackend = taskforge.BackendKind(*f.dlqBackend)
	}
	if f.redisAddr != nil {
		cfg.Redis = taskforge.RedisConfig{
			Addr:     *f.redisAddr,
			Username: *f.redisUsername,
			Password: *f.redisPassword,
			DB:       *f.redisDB,
		}
	}
	return cfg
}

func runWorker(args []string) {
	fs := flag.NewFlagSet("worker", flag.ExitOnError)
	concurrency := fs.Int("concurrency", 10, "number of concurrent workers")
	queue := fs.String("queue", "default", "queue name to consume from")
	backend := bindBackendFlags(fs, true)
	_ = fs.Parse(args)

	cfg := cliConfig()
	cfg.Concurrency = *concurrency
	cfg.DefaultQueue = *queue
	cfg = backend.apply(cfg)

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
	backend := bindBackendFlags(fs, true)
	_ = fs.Parse(args)

	if *name == "" {
		fmt.Fprintln(os.Stderr, "enqueue: -name is required")
		os.Exit(1)
	}

	cfg := cliConfig()
	cfg.DefaultQueue = *queue
	cfg = backend.apply(cfg)
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
	backend := bindBackendFlags(fs, true)
	_ = fs.Parse(args)

	if *id == "" {
		fmt.Fprintln(os.Stderr, "result: -id is required")
		os.Exit(1)
	}

	cfg := cliConfig()
	cfg = backend.apply(cfg)
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

func runDLQ(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "dlq: expected subcommand: list or get")
		os.Exit(1)
	}

	switch args[0] {
	case "list":
		runDLQList(args[1:])
	case "get":
		runDLQGet(args[1:])
	case "replay":
		runDLQReplay(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "dlq: unknown subcommand %q\n", args[0])
		os.Exit(1)
	}
}

func runDLQList(args []string) {
	fs := flag.NewFlagSet("dlq list", flag.ExitOnError)
	backend := bindBackendFlags(fs, false)
	offset := fs.Int("offset", 0, "offset into newest-first DLQ entries")
	limit := fs.Int("limit", 20, "maximum number of DLQ IDs to return")
	_ = fs.Parse(args)

	cfg := cliConfig()
	cfg = backend.apply(cfg)

	app, err := taskforge.Open(cfg)
	if err != nil {
		log.Fatalf("dlq list: %v", err)
	}
	defer app.Close() //nolint:errcheck

	ids, err := app.ListDLQEntries(context.Background(), *offset, *limit)
	if err != nil {
		log.Fatalf("dlq list: %v", err)
	}
	for _, id := range ids {
		fmt.Println(id)
	}
}

func runDLQGet(args []string) {
	fs := flag.NewFlagSet("dlq get", flag.ExitOnError)
	id := fs.String("id", "", "task ID (required)")
	backend := bindBackendFlags(fs, false)
	_ = fs.Parse(args)

	if *id == "" {
		fmt.Fprintln(os.Stderr, "dlq get: -id is required")
		os.Exit(1)
	}

	cfg := cliConfig()
	cfg = backend.apply(cfg)

	app, err := taskforge.Open(cfg)
	if err != nil {
		log.Fatalf("dlq get: %v", err)
	}
	defer app.Close() //nolint:errcheck

	entry, err := app.GetDLQEntry(context.Background(), *id)
	if err != nil {
		log.Fatalf("dlq get: %v", err)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(entry)
}

func runDLQReplay(args []string) {
	fs := flag.NewFlagSet("dlq replay", flag.ExitOnError)
	id := fs.String("id", "", "dead-lettered task ID (required)")
	queue := fs.String("queue", "", "optional queue override for the replayed task")
	delay := fs.Duration("delay", 0, "optional delay before replay runs (e.g. 5s)")
	backend := bindBackendFlags(fs, true)
	_ = fs.Parse(args)

	if *id == "" {
		fmt.Fprintln(os.Stderr, "dlq replay: -id is required")
		os.Exit(1)
	}

	cfg := cliConfig()
	cfg = backend.apply(cfg)

	app, err := taskforge.Open(cfg)
	if err != nil {
		log.Fatalf("dlq replay: %v", err)
	}
	defer app.Close() //nolint:errcheck

	var opts []taskforge.EnqueueOption
	if *queue != "" {
		opts = append(opts, taskforge.WithQueue(*queue))
	}
	if *delay > 0 {
		opts = append(opts, taskforge.WithDelay(*delay))
	}

	replayID, err := app.ReplayDLQEntry(context.Background(), *id, opts...)
	if err != nil {
		log.Fatalf("dlq replay: %v", err)
	}
	fmt.Println(replayID)
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
