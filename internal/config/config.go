// Package config loads and validates settings from environment variables.
// Invalid or unsafe combinations fail at startup.
package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Joshkaki00/4941-option-c/internal/auth"
)

// Config is the validated runtime configuration.
type Config struct {
	DatabaseURL        string
	DBMaxConns         int32
	ListenAddr         string
	MaxConcurrent      int
	EmbedProvider      string // ollama | hash
	OllamaURL          string
	EmbedModel         string
	EmbedDim           int
	LLMProvider        string // anthropic | none
	AnthropicKey       string
	AnthropicModel     string
	MaxTokens          int64
	OIDCIssuer         string
	OIDCClientID       string
	GroupsClaim        string
	Tokens             auth.StaticTokens // dev only; empty when OIDC is configured
	TopK               int
	MinSimilarity      float64
	RetrieveTimeout    time.Duration
	HandoffText        string
	WebhookSecret      []byte        // HMAC key for source-changed webhooks; empty disables them
	IngestRoot         string        // corpus directory the ingest worker reads from
	IngestPoll         time.Duration // how often the server drains the ingestion queue
	SlackBotToken      string        // empty disables the Slack bot
	SlackSigningSecret string
	SlackBotUserID     string
	SlackWorkers       int // concurrent Slack answers
	SlackQueue         int // pending Slack questions before "busy" replies
}

// Load reads configuration through getenv (os.Getenv in production, a map in tests).
func Load(getenv func(string) string) (Config, error) {
	get := func(key, def string) string {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return v
		}
		return def
	}

	cfg := Config{
		DatabaseURL:    get("DATABASE_URL", ""),
		ListenAddr:     get("LISTEN_ADDR", ":8080"),
		EmbedProvider:  get("EMBED_PROVIDER", "ollama"),
		OllamaURL:      get("OLLAMA_URL", "http://localhost:11434"),
		EmbedModel:     get("EMBED_MODEL", "nomic-embed-text"),
		LLMProvider:    get("LLM_PROVIDER", "anthropic"),
		AnthropicKey:   get("ANTHROPIC_API_KEY", ""),
		AnthropicModel: get("ANTHROPIC_MODEL", "claude-sonnet-5-5"),
		OIDCIssuer:     get("OIDC_ISSUER", ""),
		OIDCClientID:   get("OIDC_CLIENT_ID", ""),
		GroupsClaim:    get("OIDC_GROUPS_CLAIM", "groups"),
		HandoffText:    get("HANDOFF_TEXT", "Try HR, IT, or the handbook owner for this topic."),
	}
	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("config: DATABASE_URL is required")
	}

	var err error
	if cfg.EmbedDim, err = intEnv(get, "EMBED_DIM", 768); err != nil {
		return Config{}, err
	}
	if cfg.EmbedDim != 768 {
		return Config{}, fmt.Errorf("config: EMBED_DIM must be 768 to match the vector(768) column, got %d", cfg.EmbedDim)
	}
	dbConns, err := intEnv(get, "DB_MAX_CONNS", 10)
	if err != nil {
		return Config{}, err
	}
	cfg.DBMaxConns = int32(dbConns)
	if cfg.MaxConcurrent, err = intEnv(get, "MAX_CONCURRENT_ASKS", 8); err != nil {
		return Config{}, err
	}
	if cfg.TopK, err = intEnv(get, "TOP_K", 5); err != nil {
		return Config{}, err
	}
	maxTokens, err := intEnv(get, "ANTHROPIC_MAX_TOKENS", 1024)
	if err != nil {
		return Config{}, err
	}
	cfg.MaxTokens = int64(maxTokens)
	if cfg.MinSimilarity, err = floatEnv(get, "MIN_SIMILARITY", 0.5); err != nil {
		return Config{}, err
	}
	timeout, err := time.ParseDuration(get("RETRIEVE_TIMEOUT", "2s"))
	if err != nil {
		return Config{}, fmt.Errorf("config: RETRIEVE_TIMEOUT: %w", err)
	}
	cfg.RetrieveTimeout = timeout

	cfg.IngestRoot = get("INGEST_ROOT", "testdata/handbook")
	poll, err := time.ParseDuration(get("INGEST_POLL_INTERVAL", "30s"))
	if err != nil || poll <= 0 {
		return Config{}, fmt.Errorf("config: INGEST_POLL_INTERVAL must be a positive duration, got %q", get("INGEST_POLL_INTERVAL", ""))
	}
	cfg.IngestPoll = poll
	if secret := get("WEBHOOK_SECRET", ""); secret != "" {
		if len(secret) < 32 {
			return Config{}, errors.New("config: WEBHOOK_SECRET must be at least 32 characters")
		}
		cfg.WebhookSecret = []byte(secret)
	}

	cfg.SlackBotToken = get("SLACK_BOT_TOKEN", "")
	cfg.SlackSigningSecret = get("SLACK_SIGNING_SECRET", "")
	cfg.SlackBotUserID = get("SLACK_BOT_USER_ID", "")
	if cfg.SlackWorkers, err = intEnv(get, "SLACK_WORKERS", 2); err != nil {
		return Config{}, err
	}
	if cfg.SlackQueue, err = intEnv(get, "SLACK_QUEUE", 16); err != nil {
		return Config{}, err
	}
	if cfg.SlackWorkers < 1 {
		return Config{}, errors.New("config: SLACK_WORKERS must be at least 1")
	}
	if cfg.SlackQueue < 1 {
		return Config{}, errors.New("config: SLACK_QUEUE must be at least 1")
	}
	if cfg.SlackBotToken != "" {
		if cfg.SlackSigningSecret == "" {
			return Config{}, errors.New("config: SLACK_BOT_TOKEN requires SLACK_SIGNING_SECRET")
		}
		if cfg.SlackBotUserID == "" {
			return Config{}, errors.New("config: SLACK_BOT_TOKEN requires SLACK_BOT_USER_ID")
		}
	}

	switch cfg.EmbedProvider {
	case "ollama", "hash":
	default:
		return Config{}, fmt.Errorf("config: EMBED_PROVIDER must be ollama or hash, got %q", cfg.EmbedProvider)
	}
	switch cfg.LLMProvider {
	case "anthropic":
		if cfg.AnthropicKey == "" {
			return Config{}, errors.New("config: LLM_PROVIDER=anthropic requires ANTHROPIC_API_KEY")
		}
	case "none":
	default:
		return Config{}, fmt.Errorf("config: LLM_PROVIDER must be anthropic or none, got %q", cfg.LLMProvider)
	}

	devTokens := get("DEV_AUTH_TOKENS", "")
	if cfg.OIDCIssuer != "" && devTokens != "" {
		return Config{}, errors.New("config: DEV_AUTH_TOKENS must not be set when OIDC_ISSUER is configured")
	}
	if devTokens != "" {
		if cfg.Tokens, err = parseDevTokens(devTokens); err != nil {
			return Config{}, err
		}
	}
	return cfg, nil
}

// ValidateAuth is checked by the server only. The CLIs (ingest, eval) need no auth.
// It refuses to start unauthenticated.
func (c Config) ValidateAuth() error {
	switch {
	case c.OIDCIssuer != "":
		if c.OIDCClientID == "" {
			return errors.New("config: OIDC_ISSUER requires OIDC_CLIENT_ID")
		}
		return nil
	case len(c.Tokens) > 0:
		return nil
	default:
		return errors.New("config: set OIDC_ISSUER (production) or DEV_AUTH_TOKENS (local dev); refusing to start unauthenticated")
	}
}

// parseDevTokens reads "token|subject|group1,group2;token2|subject2|" entries.
func parseDevTokens(raw string) (auth.StaticTokens, error) {
	out := auth.StaticTokens{}
	for _, entry := range strings.Split(raw, ";") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.Split(entry, "|")
		if len(parts) != 3 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return nil, fmt.Errorf("config: DEV_AUTH_TOKENS entry must be token|subject|groups, got %q", entry)
		}
		var groups []string
		if g := strings.TrimSpace(parts[2]); g != "" {
			groups = strings.Split(g, ",")
		}
		out[strings.TrimSpace(parts[0])] = auth.Identity{
			Subject:   strings.TrimSpace(parts[1]),
			Audiences: auth.AudiencesFor(groups),
		}
	}
	if len(out) == 0 {
		return nil, errors.New("config: DEV_AUTH_TOKENS has no valid entries")
	}
	return out, nil
}

func intEnv(get func(string, string) string, key string, def int) (int, error) {
	n, err := strconv.Atoi(get(key, strconv.Itoa(def)))
	if err != nil {
		return 0, fmt.Errorf("config: %s must be an integer: %w", key, err)
	}
	return n, nil
}

func floatEnv(get func(string, string) string, key string, def float64) (float64, error) {
	f, err := strconv.ParseFloat(get(key, strconv.FormatFloat(def, 'f', -1, 64)), 64)
	if err != nil {
		return 0, fmt.Errorf("config: %s must be a number: %w", key, err)
	}
	return f, nil
}
