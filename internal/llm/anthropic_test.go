package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// sseBody is a minimal Messages streaming transcript: two text deltas, usage 25 in / 15 out.
const sseBody = `event: message_start
data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-5-5","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":25,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Full-time employees "}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"get 15 days [1]."}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":15}}

event: message_stop
data: {"type":"message_stop"}

`

func anthropicAgainst(t *testing.T, srv *httptest.Server) *Anthropic {
	t.Helper()
	return &Anthropic{
		client:    anthropic.NewClient(option.WithAPIKey("test-key"), option.WithBaseURL(srv.URL)),
		model:     anthropic.Model("claude-sonnet-5-5"),
		maxTokens: 256,
	}
}

func TestAnthropicStreamsTextDeltasAndUsage(t *testing.T) {
	var gotPath, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-api-key")
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseBody)
	}))
	defer srv.Close()

	a := anthropicAgainst(t, srv)
	var got strings.Builder
	usage, err := a.Generate(context.Background(), "system rules", "<passages>...</passages>\n\nQuestion: q",
		func(s string) error { got.WriteString(s); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "Full-time employees get 15 days [1]." {
		t.Fatalf("streamed text: %q", got.String())
	}
	if usage.InputTokens != 25 || usage.OutputTokens != 15 {
		t.Fatalf("usage: %+v", usage)
	}
	if gotPath != "/v1/messages" {
		t.Fatalf("wrong endpoint: %s", gotPath)
	}
	if gotKey != "test-key" {
		t.Fatal("API key not sent in x-api-key header")
	}
}

func TestAnthropicStopsWhenCallbackFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseBody)
	}))
	defer srv.Close()

	boom := errors.New("client went away")
	_, err := anthropicAgainst(t, srv).Generate(context.Background(), "s", "u", func(string) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("callback error must stop generation and be returned, got %v", err)
	}
}

func TestAnthropicUpstreamErrorIsReturned(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"type":"error","error":{"type":"overloaded_error","message":"busy"}}`)
	}))
	defer srv.Close()

	_, err := anthropicAgainst(t, srv).Generate(context.Background(), "s", "u", func(string) error { return nil })
	if err == nil {
		t.Fatal("upstream 503 must surface as an error so the caller can fall back")
	}
}

func TestNewAnthropicRequiresKey(t *testing.T) {
	if _, err := NewAnthropic("", "claude-sonnet-5-5", 256); !errors.Is(err, ErrNoAPIKey) {
		t.Fatalf("want ErrNoAPIKey, got %v", err)
	}
}
