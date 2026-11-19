// Command ingest queues every Markdown file in a corpus directory and drains the queue.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/Joshkaki00/4941-option-c/internal/app"
	"github.com/Joshkaki00/4941-option-c/internal/config"
	"github.com/Joshkaki00/4941-option-c/internal/ingest"
)

// main ingests the corpus and exits non-zero on failure.
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ingest:", err)
		os.Exit(1)
	}
}

// run parses flags, enqueues every .md file under -dir, drains the queue, and prints the counts.
func run() error {
	dir := flag.String("dir", "testdata/handbook", "corpus directory of .md files with front matter")
	concurrency := flag.Int("concurrency", 2, "parallel ingestion jobs")
	flag.Parse()

	abs, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	if err := config.LoadDotEnv(".env"); err != nil {
		return err
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	rt, err := app.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer rt.Close()

	queued, err := ingest.EnqueueDir(ctx, rt.Store, abs)
	if err != nil {
		return fmt.Errorf("enqueue: %w", err)
	}
	w := &ingest.Worker{
		Store:       rt.Store,
		Embedder:    rt.Embedder,
		Root:        abs,
		Concurrency: *concurrency,
		Logger:      rt.Logger,
	}
	done, failed, err := w.Drain(ctx)
	fmt.Printf("queued=%d done=%d failed=%d\n", queued, done, failed)
	if err != nil {
		return err
	}
	if failed > 0 {
		return fmt.Errorf("%d job(s) failed; see logs", failed)
	}
	return nil
}
