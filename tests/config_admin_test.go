package tests

import (
	"strings"
	"testing"
	"time"

	"github.com/Joshkaki00/4941-option-c/internal/config"
)

func TestWebhookSecretDisabledByDefault(t *testing.T) {
	cfg := mustLoad(t, base())
	if len(cfg.WebhookSecret) != 0 {
		t.Fatal("webhook must be disabled when WEBHOOK_SECRET is unset")
	}
}

func TestWebhookSecretMustBeAtLeast32Bytes(t *testing.T) {
	m := base()
	m["WEBHOOK_SECRET"] = "short"
	wantLoadErr(t, m, "WEBHOOK_SECRET must be at least 32 characters")
}

func TestWebhookSecretAcceptedWhenLongEnough(t *testing.T) {
	m := base()
	m["WEBHOOK_SECRET"] = strings.Repeat("k", 32)
	cfg := mustLoad(t, m)
	if string(cfg.WebhookSecret) != strings.Repeat("k", 32) {
		t.Fatal("secret not loaded")
	}
}

func TestIngestRootDefaultsToSampleCorpus(t *testing.T) {
	cfg := mustLoad(t, base())
	if cfg.IngestRoot != "testdata/handbook" {
		t.Fatalf("ingest root = %q", cfg.IngestRoot)
	}
}

func TestIngestPollIntervalParsesAndDefaults(t *testing.T) {
	cfg := mustLoad(t, base())
	if cfg.IngestPoll != 30*time.Second {
		t.Fatalf("default poll = %v", cfg.IngestPoll)
	}
	m := base()
	m["INGEST_POLL_INTERVAL"] = "5s"
	if got := mustLoad(t, m).IngestPoll; got != 5*time.Second {
		t.Fatalf("poll = %v, want 5s", got)
	}
	m["INGEST_POLL_INTERVAL"] = "0s"
	if _, err := config.Load(env(m)); err == nil {
		t.Fatal("zero poll interval must be rejected (the worker would spin)")
	}
}
