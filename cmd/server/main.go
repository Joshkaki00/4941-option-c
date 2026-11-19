// Command server runs the Ask the Handbook HTTP API and its ingestion worker.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Joshkaki00/4941-option-c/internal/admin"
	"github.com/Joshkaki00/4941-option-c/internal/app"
	"github.com/Joshkaki00/4941-option-c/internal/config"
	"github.com/Joshkaki00/4941-option-c/internal/httpapi"
	"github.com/Joshkaki00/4941-option-c/internal/ingest"
	"github.com/Joshkaki00/4941-option-c/internal/slack"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "server:", err)
		os.Exit(1)
	}
}

func run() error {
	if err := config.LoadDotEnv(".env"); err != nil {
		return err
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}

	// Validate auth before touching the database: the server must never start unauthenticated.
	if err := cfg.ValidateAuth(); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	rt, err := app.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer rt.Close()

	verifier, err := app.BuildVerifier(ctx, cfg)
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	if cfg.OIDCIssuer == "" {
		rt.Logger.Warn("dev auth enabled: static tokens accepted; do not run this in production")
	}

	// One ingestion worker goroutine. It is cancelled and joined before the store closes.
	worker := &ingest.Worker{Store: rt.Store, Embedder: rt.Embedder, Root: cfg.IngestRoot, Logger: rt.Logger}
	loopCtx, cancelLoop := context.WithCancel(ctx)
	loopDone := make(chan struct{})
	go func() {
		defer close(loopDone)
		runIngestLoop(loopCtx, worker, cfg.IngestPoll, rt.Logger)
	}()
	defer func() {
		cancelLoop()
		<-loopDone
	}()

	api := httpapi.New(httpapi.Deps{
		Verifier:      verifier,
		Store:         rt.Store,
		Chunks:        rt.Searcher,
		Asker:         rt.Answers,
		Logger:        rt.Logger,
		MaxConcurrent: cfg.MaxConcurrent,
	})
	adm := admin.New(admin.Deps{
		Verifier: verifier,
		Store:    rt.Store,
		EnqueueCorpus: func(ctx context.Context) (int, error) {
			return ingest.EnqueueDir(ctx, rt.Store, cfg.IngestRoot)
		},
		WebhookSecret: cfg.WebhookSecret,
		Logger:        rt.Logger,
	})
	mux := http.NewServeMux()
	mux.Handle("/v1/admin/", adm.Handler())
	mux.Handle("/", api.Handler())

	if cfg.SlackBotToken != "" {
		// Slack workers get their own context so they stop before the store closes.
		bot := slack.NewBot(slack.Config{
			SigningSecret: cfg.SlackSigningSecret,
			BotUserID:     cfg.SlackBotUserID,
			Workers:       cfg.SlackWorkers,
			QueueSize:     cfg.SlackQueue,
		}, slack.NewAPIClient(cfg.SlackBotToken), rt.Store, rt.Answers, rt.Logger)
		slackCtx, cancelSlack := context.WithCancel(ctx)
		slackDone := make(chan struct{})
		go func() {
			defer close(slackDone)
			bot.Run(slackCtx)
		}()
		defer func() {
			cancelSlack()
			<-slackDone
		}()
		mux.Handle("/slack/", bot.Handler())
		rt.Logger.Info("slack bot enabled", "workers", cfg.SlackWorkers, "queue", cfg.SlackQueue)
	}

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
		// No global WriteTimeout: SSE responses set their own write deadline.
	}

	errCh := make(chan error, 1)
	go func() {
		rt.Logger.Info("listening", "addr", cfg.ListenAddr, "llm", rt.Answers.Model)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	rt.Logger.Info("shutting down")
	return srv.Shutdown(shutdownCtx)
}

// runIngestLoop drains the ingestion queue on every tick until ctx is cancelled.
func runIngestLoop(ctx context.Context, w *ingest.Worker, every time.Duration, log interface {
	Error(msg string, args ...any)
}) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, _, err := w.Drain(ctx); err != nil && ctx.Err() == nil {
				log.Error("ingest drain failed", "err", err)
			}
		}
	}
}
