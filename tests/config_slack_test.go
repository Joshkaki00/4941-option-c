package tests

import (
	"strings"
	"testing"

	"github.com/Joshkaki00/4941-option-c/internal/config"
)

func TestSlackDisabledByDefault(t *testing.T) {
	cfg := mustLoad(t, base())
	if cfg.SlackBotToken != "" {
		t.Fatal("Slack must be off unless SLACK_BOT_TOKEN is set")
	}
}

func TestSlackTokenRequiresSigningSecretAndBotUserID(t *testing.T) {
	m := base()
	m["SLACK_BOT_TOKEN"] = "xoxb-test"
	wantLoadErr(t, m, "SLACK_SIGNING_SECRET")

	m["SLACK_SIGNING_SECRET"] = "signing-secret"
	wantLoadErr(t, m, "SLACK_BOT_USER_ID")

	m["SLACK_BOT_USER_ID"] = "UBOT"
	cfg := mustLoad(t, m)
	if cfg.SlackBotToken != "xoxb-test" || cfg.SlackSigningSecret != "signing-secret" || cfg.SlackBotUserID != "UBOT" {
		t.Fatalf("slack config = %+v", cfg)
	}
	if cfg.SlackWorkers != 2 || cfg.SlackQueue != 16 {
		t.Fatalf("defaults: workers=%d queue=%d", cfg.SlackWorkers, cfg.SlackQueue)
	}
}

func TestSlackWorkersAndQueueMustBePositive(t *testing.T) {
	m := base()
	m["SLACK_BOT_TOKEN"] = "xoxb-test"
	m["SLACK_SIGNING_SECRET"] = "s"
	m["SLACK_BOT_USER_ID"] = "UBOT"
	m["SLACK_WORKERS"] = "0"
	wantLoadErr(t, m, "SLACK_WORKERS")

	m["SLACK_WORKERS"] = "2"
	m["SLACK_QUEUE"] = "-1"
	wantLoadErr(t, m, "SLACK_QUEUE")
}

func TestSlackSecretsAreNotEchoedInErrors(t *testing.T) {
	m := base()
	m["SLACK_BOT_TOKEN"] = "xoxb-secret-value"
	_, err := config.Load(env(m))
	if err != nil && strings.Contains(err.Error(), "xoxb-secret-value") {
		t.Fatalf("error leaks the bot token: %v", err)
	}
}
