package config

import (
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func base() map[string]string {
	return map[string]string{
		"DATABASE_URL":    "postgres:///handbook",
		"DEV_AUTH_TOKENS": "dev-alice|alice|engineering",
		"LLM_PROVIDER":    "none",
		"EMBED_PROVIDER":  "hash",
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(env(base()))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != ":8080" || cfg.TopK != 5 || cfg.MinSimilarity != 0.5 {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.Tokens["dev-alice"].Subject != "alice" {
		t.Fatalf("dev token not parsed: %+v", cfg.Tokens)
	}
}

func TestLoadRequiresDatabaseURL(t *testing.T) {
	m := base()
	delete(m, "DATABASE_URL")
	if _, err := Load(env(m)); err == nil {
		t.Fatal("want error without DATABASE_URL")
	}
}

func TestValidateAuthRefusesToStartWithoutAnyAuth(t *testing.T) {
	m := base()
	delete(m, "DEV_AUTH_TOKENS")
	cfg, err := Load(env(m))
	if err != nil {
		t.Fatalf("Load must not require auth (CLIs need none): %v", err)
	}
	if cfg.ValidateAuth() == nil {
		t.Fatal("server must not start without OIDC or dev tokens")
	}
}

func TestLoadAllowsCLIWithoutAuth(t *testing.T) {
	m := base()
	delete(m, "DEV_AUTH_TOKENS")
	if _, err := Load(env(m)); err != nil {
		t.Fatalf("ingest/eval must load without auth config: %v", err)
	}
}

func TestLoadRefusesDevTokensAlongsideOIDC(t *testing.T) {
	m := base()
	m["OIDC_ISSUER"] = "https://sso.example.com"
	m["OIDC_CLIENT_ID"] = "handbook"
	if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), "DEV_AUTH_TOKENS") {
		t.Fatalf("dev tokens must not be combined with OIDC, got %v", err)
	}
}

func TestLoadOIDCRequiresClientID(t *testing.T) {
	m := base()
	delete(m, "DEV_AUTH_TOKENS")
	m["OIDC_ISSUER"] = "https://sso.example.com"
	cfg, err := Load(env(m))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ValidateAuth() == nil {
		t.Fatal("OIDC issuer without client id must fail")
	}
}

func TestLoadAnthropicRequiresKey(t *testing.T) {
	m := base()
	m["LLM_PROVIDER"] = "anthropic"
	if _, err := Load(env(m)); err == nil {
		t.Fatal("anthropic provider without ANTHROPIC_API_KEY must fail")
	}
	m["ANTHROPIC_API_KEY"] = "sk-test"
	if _, err := Load(env(m)); err != nil {
		t.Fatalf("with key: %v", err)
	}
}

func TestLoadRejectsUnknownProviders(t *testing.T) {
	m := base()
	m["EMBED_PROVIDER"] = "magic"
	if _, err := Load(env(m)); err == nil {
		t.Fatal("unknown embed provider must fail")
	}
}

func TestParseDevTokensMultipleEntriesAndGroups(t *testing.T) {
	m := base()
	m["DEV_AUTH_TOKENS"] = "t1|alice|engineering,managers; t2|bob|"
	cfg, err := Load(env(m))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cfg.Tokens["t1"].Audiences, ","); got != "all-employees,engineering,managers" {
		t.Fatalf("t1 audiences: %s", got)
	}
	if got := strings.Join(cfg.Tokens["t2"].Audiences, ","); got != "all-employees" {
		t.Fatalf("t2 audiences: %s", got)
	}
}

func TestParseDevTokensRejectsMalformed(t *testing.T) {
	m := base()
	m["DEV_AUTH_TOKENS"] = "just-a-token"
	if _, err := Load(env(m)); err == nil {
		t.Fatal("malformed dev token must fail")
	}
}
