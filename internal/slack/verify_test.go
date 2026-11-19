package slack_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/Joshkaki00/4941-option-c/internal/slack"
)

const signingSecret = "test-signing-secret"

func slackSig(secret, ts string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte("v0:" + ts + ":"))
	m.Write(body)
	return "v0=" + hex.EncodeToString(m.Sum(nil))
}

func TestVerifyAcceptsValidSignature(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	ts := strconv.FormatInt(now.Unix(), 10)
	body := []byte(`{"type":"event_callback"}`)
	if err := slack.Verify(signingSecret, body, ts, slackSig(signingSecret, ts, body), now); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
}

func TestVerifyRejectsTamperedBodyAndWrongSecret(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	ts := strconv.FormatInt(now.Unix(), 10)
	body := []byte(`{"type":"event_callback"}`)
	sig := slackSig(signingSecret, ts, body)
	if err := slack.Verify(signingSecret, []byte(`{"type":"other"}`), ts, sig, now); !errors.Is(err, slack.ErrBadSignature) {
		t.Fatalf("tampered body: err = %v", err)
	}
	if err := slack.Verify("wrong-secret", body, ts, sig, now); !errors.Is(err, slack.ErrBadSignature) {
		t.Fatalf("wrong secret: err = %v", err)
	}
}

func TestVerifyRejectsStaleAndFutureTimestamps(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	body := []byte(`{}`)
	for name, skew := range map[string]time.Duration{
		"stale":  -6 * time.Minute,
		"future": 6 * time.Minute,
	} {
		ts := strconv.FormatInt(now.Add(skew).Unix(), 10)
		err := slack.Verify(signingSecret, body, ts, slackSig(signingSecret, ts, body), now)
		if !errors.Is(err, slack.ErrStaleTimestamp) {
			t.Errorf("%s: err = %v, want ErrStaleTimestamp", name, err)
		}
	}
}

func TestVerifyAcceptsWithinFiveMinutes(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	body := []byte(`{}`)
	ts := strconv.FormatInt(now.Add(-4*time.Minute).Unix(), 10)
	if err := slack.Verify(signingSecret, body, ts, slackSig(signingSecret, ts, body), now); err != nil {
		t.Fatalf("4 minutes old should pass: %v", err)
	}
}

func TestVerifyRejectsMissingOrMalformedHeaders(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	body := []byte(`{}`)
	ts := strconv.FormatInt(now.Unix(), 10)
	cases := map[string][2]string{
		"missing timestamp": {"", slackSig(signingSecret, ts, body)},
		"non-numeric ts":    {"yesterday", slackSig(signingSecret, "yesterday", body)},
		"missing signature": {ts, ""},
		"no v0 prefix":      {ts, hex.EncodeToString([]byte("abc"))},
		"non-hex signature": {ts, "v0=zz"},
	}
	for name, c := range cases {
		if err := slack.Verify(signingSecret, body, c[0], c[1], now); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
