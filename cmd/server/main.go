// Command server runs the Ask the Handbook HTTP API.
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

	"github.com/Joshkaki00/4941-option-c/internal/app"
	"github.com/Joshkaki00/4941-option-c/internal/config"
	"github.com/Joshkaki00/4941-option-c/internal/httpapi"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "server:", err)
		os.Exit(1)
	}
}

func run() error {
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

	api := httpapi.New(httpapi.Deps{
		Verifier:      verifier,
		Store:         rt.Store,
		Chunks:        rt.Searcher,
		Asker:         rt.Answers,
		Logger:        rt.Logger,
		MaxConcurrent: cfg.MaxConcurrent,
	})

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           api.Handler(),
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
