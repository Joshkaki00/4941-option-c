package tests

import (
	"strings"
	"testing"
	"time"

	"github.com/Joshkaki00/4941-option-c/internal/config"
)

// env returns a getenv over a map, the same shape config.Load receives in production.
func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// base is a valid, retrieval-only configuration (no API key needed).
func base() map[string]string {
	return map[string]string{
		"DATABASE_URL":   "postgres:///handbook",
		"LLM_PROVIDER":   "none",
		"EMBED_PROVIDER": "hash",
	}
}

func mustLoad(t *testing.T, m map[string]string) config.Config {
	t.Helper()
	cfg, err := config.Load(env(m))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

func wantLoadErr(t *testing.T, m map[string]string, contains string) {
	t.Helper()
	_, err := config.Load(env(m))
	if err == nil || !strings.Contains(err.Error(), contains) {
		t.Fatalf("want error containing %q, got %v", contains, err)
	}
}

func TestLoadAppliesDefaults(t *testing.T) {
	cfg := mustLoad(t, base())
	if cfg.ListenAddr != ":8080" || cfg.EmbedDim != 768 || cfg.TopK != 5 {
		t.Fatalf("defaults: addr=%q dim=%d topK=%d", cfg.ListenAddr, cfg.EmbedDim, cfg.TopK)
	}
	if cfg.MaxConcurrent != 8 || cfg.DBMaxConns != 10 {
		t.Fatalf("pool defaults: concurrent=%d conns=%d", cfg.MaxConcurrent, cfg.DBMaxConns)
	}
	if cfg.RetrieveTimeout != 2*time.Second {
		t.Fatalf("retrieve timeout = %v", cfg.RetrieveTimeout)
	}
	if cfg.MinSimilarity != 0.5 {
		t.Fatalf("min similarity = %v", cfg.MinSimilarity)
	}
	if cfg.GroupsClaim != "groups" {
		t.Fatalf("groups claim = %q", cfg.GroupsClaim)
	}
}

func TestLoadRequiresDatabaseURL(t *testing.T) {
	m := base()
	delete(m, "DATABASE_URL")
	wantLoadErr(t, m, "DATABASE_URL is required")
}

func TestLoadTreatsWhitespaceOnlyAsUnset(t *testing.T) {
	m := base()
	m["DATABASE_URL"] = "   "
	wantLoadErr(t, m, "DATABASE_URL is required")
}

func TestLoadRejectsEmbedDimOtherThan768(t *testing.T) {
	m := base()
	m["EMBED_DIM"] = "384"
	wantLoadErr(t, m, "must be 768")
}

func TestLoadRejectsNonNumericIntegers(t *testing.T) {
	for _, key := range []string{"EMBED_DIM", "DB_MAX_CONNS", "MAX_CONCURRENT_ASKS", "TOP_K", "ANTHROPIC_MAX_TOKENS"} {
		m := base()
		m[key] = "ten"
		wantLoadErr(t, m, key)
	}
}

func TestLoadRejectsBadFloatAndDuration(t *testing.T) {
	m := base()
	m["MIN_SIMILARITY"] = "high"
	wantLoadErr(t, m, "MIN_SIMILARITY")

	m = base()
	m["RETRIEVE_TIMEOUT"] = "two seconds"
	wantLoadErr(t, m, "RETRIEVE_TIMEOUT")
}

func TestLoadRejectsUnknownEmbedProvider(t *testing.T) {
	m := base()
	m["EMBED_PROVIDER"] = "openai"
	wantLoadErr(t, m, "EMBED_PROVIDER")
}

func TestLoadAnthropicRequiresKey(t *testing.T) {
	m := base()
	m["LLM_PROVIDER"] = "anthropic"
	wantLoadErr(t, m, "requires ANTHROPIC_API_KEY")

	m["ANTHROPIC_API_KEY"] = "sk-test"
	cfg := mustLoad(t, m)
	if cfg.AnthropicKey != "sk-test" || cfg.AnthropicModel != "claude-sonnet-5-5" {
		t.Fatalf("anthropic config = %q / %q", cfg.AnthropicKey, cfg.AnthropicModel)
	}
}

func TestLoadRejectsUnknownLLMProvider(t *testing.T) {
	m := base()
	m["LLM_PROVIDER"] = "ollama"
	wantLoadErr(t, m, "LLM_PROVIDER")
}

func TestLoadParsesDevTokens(t *testing.T) {
	m := base()
	m["DEV_AUTH_TOKENS"] = "dev-alice|alice|hr-only, eng ; dev-bob|bob|"
	cfg := mustLoad(t, m)
	alice, ok := cfg.Tokens["dev-alice"]
	if !ok || alice.Subject != "alice" {
		t.Fatalf("alice token missing: %+v", cfg.Tokens)
	}
	got := strings.Join(alice.Audiences, ",")
	if got != "all-employees,eng,hr-only" {
		t.Fatalf("alice audiences = %q", got)
	}
	bob := cfg.Tokens["dev-bob"]
	if strings.Join(bob.Audiences, ",") != "all-employees" {
		t.Fatalf("bob with no groups = %v", bob.Audiences)
	}
}

func TestLoadRejectsMalformedDevTokens(t *testing.T) {
	for _, raw := range []string{"just-a-token", "tok|", "|subject|grp", "a|b|c|d"} {
		m := base()
		m["DEV_AUTH_TOKENS"] = raw
		_, err := config.Load(env(m))
		if err == nil {
			t.Errorf("DEV_AUTH_TOKENS %q should fail", raw)
		}
	}
}

func TestLoadRejectsDevTokensWithOIDC(t *testing.T) {
	m := base()
	m["OIDC_ISSUER"] = "https://login.example.com"
	m["DEV_AUTH_TOKENS"] = "dev|alice|"
	wantLoadErr(t, m, "must not be set when OIDC_ISSUER")
}

func TestValidateAuthRefusesUnauthenticatedStart(t *testing.T) {
	cfg := mustLoad(t, base())
	if err := cfg.ValidateAuth(); err == nil || !strings.Contains(err.Error(), "refusing to start unauthenticated") {
		t.Fatalf("no auth configured must refuse to start, got %v", err)
	}
}

func TestValidateAuthAcceptsDevTokens(t *testing.T) {
	m := base()
	m["DEV_AUTH_TOKENS"] = "dev|alice|"
	if err := mustLoad(t, m).ValidateAuth(); err != nil {
		t.Fatalf("dev tokens should satisfy auth: %v", err)
	}
}

func TestValidateAuthOIDCNeedsClientID(t *testing.T) {
	m := base()
	m["OIDC_ISSUER"] = "https://login.example.com"
	cfg := mustLoad(t, m)
	if err := cfg.ValidateAuth(); err == nil || !strings.Contains(err.Error(), "OIDC_CLIENT_ID") {
		t.Fatalf("OIDC without client id must fail, got %v", err)
	}

	m["OIDC_CLIENT_ID"] = "handbook"
	if err := mustLoad(t, m).ValidateAuth(); err != nil {
		t.Fatalf("OIDC with client id should pass: %v", err)
	}
}

func TestLoadDoesNotRequireAuth(t *testing.T) {
	// CLIs (ingest, eval) load config without any auth configured.
	if _, err := config.Load(env(base())); err != nil {
		t.Fatalf("Load must not require auth: %v", err)
	}
}
