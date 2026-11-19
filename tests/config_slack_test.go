package tests

import (
	"strings"
	"testing"

	"github.com/Joshkaki00/4941-option-c/internal/config"
)

func TestSlackDisabledByDefault(t *testing.T) {
	cfg := mustLoad(t, base())
	if cfg.SlackBotToken != "" || cfg.SlackAppToken != "" {
		t.Fatal("Slack must be off unless SLACK_BOT_TOKEN and SLACK_APP_TOKEN are set")
	}
}

func TestSlackSocketModeNeedsBothTokens(t *testing.T) {
	m := base()
	m["SLACK_BOT_TOKEN"] = "xoxb-test"
	wantLoadErr(t, m, "SLACK_APP_TOKEN")

	m = base()
	m["SLACK_APP_TOKEN"] = "xapp-test"
	wantLoadErr(t, m, "SLACK_APP_TOKEN requires SLACK_BOT_TOKEN")

	m["SLACK_BOT_TOKEN"] = "xoxb-test"
	cfg := mustLoad(t, m)
	if cfg.SlackBotToken != "xoxb-test" || cfg.SlackAppToken != "xapp-test" {
		t.Fatalf("slack tokens = %q / %q", cfg.SlackBotToken, cfg.SlackAppToken)
	}
	if cfg.SlackWorkers != 2 || cfg.SlackQueue != 16 {
		t.Fatalf("defaults: workers=%d queue=%d", cfg.SlackWorkers, cfg.SlackQueue)
	}
}

func TestSlackWorkersAndQueueMustBePositive(t *testing.T) {
	m := base()
	m["SLACK_BOT_TOKEN"] = "xoxb-test"
	m["SLACK_APP_TOKEN"] = "xapp-test"
	m["SLACK_WORKERS"] = "0"
	wantLoadErr(t, m, "SLACK_WORKERS")

	m["SLACK_WORKERS"] = "2"
	m["SLACK_QUEUE"] = "-1"
	wantLoadErr(t, m, "SLACK_QUEUE")
}

func TestSlackTokensAreNotEchoedInErrors(t *testing.T) {
	m := base()
	m["SLACK_BOT_TOKEN"] = "xoxb-secret-value"
	_, err := config.Load(env(m))
	if err != nil && strings.Contains(err.Error(), "xoxb-secret-value") {
		t.Fatalf("error leaks the bot token: %v", err)
	}
}
