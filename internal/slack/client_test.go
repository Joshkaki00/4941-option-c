package slack_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Joshkaki00/4941-option-c/internal/slack"
)

type recorded struct {
	path, auth string
	body       map[string]any
}

func fakeSlackAPI(t *testing.T, reply string) (*httptest.Server, *[]recorded) {
	t.Helper()
	var calls []recorded
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		calls = append(calls, recorded{path: r.URL.Path, auth: r.Header.Get("Authorization"), body: body})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestPostMessageSendsBearerAndReturnsTimestamp(t *testing.T) {
	srv, calls := fakeSlackAPI(t, `{"ok":true,"ts":"1700.01"}`)
	c := &slack.APIClient{BaseURL: srv.URL, Token: "xoxb-test", HTTP: srv.Client()}

	ts, err := c.PostMessage(context.Background(), "C1", "1699.9", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if ts != "1700.01" {
		t.Fatalf("ts = %q", ts)
	}
	got := (*calls)[0]
	if got.path != "/chat.postMessage" || got.auth != "Bearer xoxb-test" {
		t.Fatalf("call = %+v", got)
	}
	if got.body["channel"] != "C1" || got.body["thread_ts"] != "1699.9" || got.body["text"] != "hello" {
		t.Fatalf("body = %v", got.body)
	}
}

func TestPostMessageOmitsEmptyThread(t *testing.T) {
	srv, calls := fakeSlackAPI(t, `{"ok":true,"ts":"1"}`)
	c := &slack.APIClient{BaseURL: srv.URL, Token: "t", HTTP: srv.Client()}
	if _, err := c.PostMessage(context.Background(), "C1", "", "hi"); err != nil {
		t.Fatal(err)
	}
	if _, present := (*calls)[0].body["thread_ts"]; present {
		t.Fatal("thread_ts must be omitted for a top-level post")
	}
}

func TestSlackErrorsSurface(t *testing.T) {
	srv, _ := fakeSlackAPI(t, `{"ok":false,"error":"channel_not_found"}`)
	c := &slack.APIClient{BaseURL: srv.URL, Token: "t", HTTP: srv.Client()}
	err := c.UpdateMessage(context.Background(), "C1", "1.0", "x")
	if err == nil || !strings.Contains(err.Error(), "channel_not_found") {
		t.Fatalf("err = %v", err)
	}
}

func TestUpdateMessageCallsChatUpdate(t *testing.T) {
	srv, calls := fakeSlackAPI(t, `{"ok":true}`)
	c := &slack.APIClient{BaseURL: srv.URL, Token: "t", HTTP: srv.Client()}
	if err := c.UpdateMessage(context.Background(), "C1", "1700.01", "final"); err != nil {
		t.Fatal(err)
	}
	got := (*calls)[0]
	if got.path != "/chat.update" || got.body["ts"] != "1700.01" || got.body["text"] != "final" {
		t.Fatalf("call = %+v", got)
	}
}
