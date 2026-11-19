package tests

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Joshkaki00/4941-option-c/internal/answer"
	"github.com/Joshkaki00/4941-option-c/internal/auth"
	"github.com/Joshkaki00/4941-option-c/internal/slack"
)

func v0Sig(secret, ts string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte("v0:" + ts + ":"))
	m.Write(body)
	return "v0=" + hex.EncodeToString(m.Sum(nil))
}

func TestSlackVerifyAcceptsCurrentSignature(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	ts := strconv.FormatInt(now.Unix(), 10)
	body := []byte(`{"type":"event_callback"}`)
	if err := slack.Verify("sec", body, ts, v0Sig("sec", ts, body), now); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
}

func TestSlackVerifyWindowIsFiveMinutesEachWay(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	body := []byte(`{}`)
	for _, tc := range []struct {
		skew time.Duration
		want error
	}{
		{5 * time.Minute, nil},
		{-5 * time.Minute, nil},
		{5*time.Minute + time.Second, slack.ErrStaleTimestamp},
		{-(5*time.Minute + time.Second), slack.ErrStaleTimestamp},
	} {
		ts := strconv.FormatInt(now.Add(tc.skew).Unix(), 10)
		err := slack.Verify("sec", body, ts, v0Sig("sec", ts, body), now)
		if tc.want == nil && err != nil {
			t.Errorf("skew %v: %v", tc.skew, err)
		}
		if tc.want != nil && !errors.Is(err, tc.want) {
			t.Errorf("skew %v: err = %v, want %v", tc.skew, err, tc.want)
		}
	}
}

func TestSlackVerifyRejectsForgery(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	ts := strconv.FormatInt(now.Unix(), 10)
	body := []byte(`{"type":"event_callback"}`)
	sig := v0Sig("sec", ts, body)
	if err := slack.Verify("sec", []byte(`{"type":"x"}`), ts, sig, now); !errors.Is(err, slack.ErrBadSignature) {
		t.Fatalf("altered body accepted: %v", err)
	}
	if err := slack.Verify("other", body, ts, sig, now); !errors.Is(err, slack.ErrBadSignature) {
		t.Fatalf("wrong secret accepted: %v", err)
	}
	if err := slack.Verify("sec", body, ts, "v1="+strings.TrimPrefix(sig, "v0="), now); !errors.Is(err, slack.ErrBadSignature) {
		t.Fatalf("wrong scheme prefix accepted: %v", err)
	}
}

func TestSlackQuestionFromMention(t *testing.T) {
	q, ok := slack.Question(slack.InnerEvent{Type: "app_mention", Text: "<@UBOT> pto days?"}, "UBOT")
	if !ok || q != "pto days?" {
		t.Fatalf("mention question = %q, ok = %v", q, ok)
	}
	q, ok = slack.Question(slack.InnerEvent{Type: "app_mention", Text: "hey <@UBOT> how much pto"}, "UBOT")
	if !ok || q != "hey  how much pto" {
		t.Fatalf("mid-text mention = %q", q)
	}
}

func TestSlackQuestionRejectsBotLoopsAndEmpty(t *testing.T) {
	for name, ev := range map[string]slack.InnerEvent{
		"bot reply to itself": {Type: "message", ChannelType: "im", Text: "x", BotID: "B1"},
		"message edit":        {Type: "message", ChannelType: "im", Text: "x", Subtype: "message_changed"},
		"empty dm":            {Type: "message", ChannelType: "im", Text: " \n "},
		"public channel":      {Type: "message", ChannelType: "channel", Text: "pto?"},
		"reaction":            {Type: "reaction_added", Text: "x"},
	} {
		if q, ok := slack.Question(ev, "UBOT"); ok {
			t.Errorf("%s produced %q", name, q)
		}
	}
}

func TestSlackFormatAnsweredListsSourcesAndDate(t *testing.T) {
	res := answer.Result{
		Outcome: answer.Answered,
		Answer:  "15 days [1].",
		Citations: []answer.Citation{{
			DocumentTitle: "PTO Policy", Section: "Accrual",
			LastUpdated: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		}},
	}
	text := slack.FormatAnswer(res)
	if !strings.HasPrefix(text, "15 days [1].") {
		t.Fatalf("answer must lead: %q", text)
	}
	if !strings.Contains(text, "• PTO Policy › Accrual (updated 2026-03-01)") {
		t.Fatalf("source line missing: %q", text)
	}
}

func TestSlackFormatDeclineHasNoSourcesButKeepsHandoff(t *testing.T) {
	res := answer.Result{
		Outcome:   answer.DeclinedNoSource,
		Answer:    answer.DeclineText,
		Handoff:   "Try HR.",
		Citations: []answer.Citation{{DocumentTitle: "Should Not Show"}},
	}
	text := slack.FormatAnswer(res)
	if strings.Contains(text, "Should Not Show") || strings.Contains(text, "Sources:") {
		t.Fatalf("decline leaked sources: %q", text)
	}
	if !strings.HasSuffix(text, "Try HR.") {
		t.Fatalf("handoff missing: %q", text)
	}
}

func TestSlackCopyIsNonEmpty(t *testing.T) {
	for name, s := range map[string]string{
		"placeholder": slack.PlaceholderText, "failure": slack.FailureText, "busy": slack.BusyText,
	} {
		if strings.TrimSpace(s) == "" {
			t.Errorf("%s copy is empty", name)
		}
	}
}

func TestSlackAudiencesAreCompanyWideOnly(t *testing.T) {
	got := slack.AudiencesFor("U123")
	if len(got) != 1 || got[0] != auth.CompanyWideAudience {
		t.Fatalf("slack audiences = %v; restricted audiences need an explicit mapping", got)
	}
}
