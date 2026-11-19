// Package app builds the runtime dependency graph from configuration.
// It is shared by the server, the ingest CLI, and the eval CLI.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/Joshkaki00/4941-option-c/internal/answer"
	"github.com/Joshkaki00/4941-option-c/internal/auth"
	"github.com/Joshkaki00/4941-option-c/internal/config"
	"github.com/Joshkaki00/4941-option-c/internal/embed"
	"github.com/Joshkaki00/4941-option-c/internal/llm"
	"github.com/Joshkaki00/4941-option-c/internal/retrieval"
	"github.com/Joshkaki00/4941-option-c/internal/store"
)

// Runtime holds the shared components.
type Runtime struct {
	Config    config.Config
	Store     *store.Store
	Embedder  embed.Embedder
	Generator llm.Generator // nil when LLM_PROVIDER=none (retrieval-only tier)
	Searcher  *retrieval.Searcher
	Answers   *answer.Service
	Logger    *slog.Logger
}

// Open migrates the database, opens the pool, and wires the components.
func Open(ctx context.Context, cfg config.Config) (*Runtime, error) {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := store.Migrate(ctx, cfg.DatabaseURL); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	st, err := store.Open(ctx, cfg.DatabaseURL, cfg.DBMaxConns)
	if err != nil {
		return nil, err
	}

	emb, err := BuildEmbedder(cfg)
	if err != nil {
		st.Close()
		return nil, err
	}
	gen, err := BuildGenerator(cfg)
	if err != nil {
		st.Close()
		return nil, err
	}

	searcher := retrieval.New(st, emb)
	svc := &answer.Service{
		Retriever:       searcher,
		Generator:       gen,
		Model:           modelName(cfg),
		TopK:            cfg.TopK,
		MinSim:          cfg.MinSimilarity,
		RetrieveTimeout: cfg.RetrieveTimeout,
		Handoff:         cfg.HandoffText,
	}
	return &Runtime{
		Config: cfg, Store: st, Embedder: emb, Generator: gen,
		Searcher: searcher, Answers: svc, Logger: logger,
	}, nil
}

// Close releases resources.
func (r *Runtime) Close() { r.Store.Close() }

// BuildEmbedder returns the configured embedder.
func BuildEmbedder(cfg config.Config) (embed.Embedder, error) {
	switch cfg.EmbedProvider {
	case "ollama":
		return embed.NewOllama(cfg.OllamaURL, cfg.EmbedModel, cfg.EmbedDim), nil
	case "hash":
		return embed.NewHash(cfg.EmbedDim), nil
	default:
		return nil, fmt.Errorf("unknown embed provider %q", cfg.EmbedProvider)
	}
}

// BuildGenerator returns the hosted generator, or nil for the retrieval-only tier.
func BuildGenerator(cfg config.Config) (llm.Generator, error) {
	switch cfg.LLMProvider {
	case "anthropic":
		return llm.NewAnthropic(cfg.AnthropicKey, cfg.AnthropicModel, cfg.MaxTokens)
	case "none":
		return nil, nil
	default:
		return nil, fmt.Errorf("unknown llm provider %q", cfg.LLMProvider)
	}
}

// BuildVerifier returns the OIDC verifier, or the dev static-token verifier.
// config.Load guarantees exactly one is configured.
func BuildVerifier(ctx context.Context, cfg config.Config) (auth.Verifier, error) {
	if cfg.OIDCIssuer != "" {
		return auth.NewOIDC(ctx, cfg.OIDCIssuer, cfg.OIDCClientID, cfg.GroupsClaim)
	}
	return cfg.Tokens, nil
}

func modelName(cfg config.Config) string {
	if cfg.LLMProvider == "none" {
		return "retrieval-only"
	}
	return cfg.AnthropicModel
}
