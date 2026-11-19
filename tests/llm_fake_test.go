package tests

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/Joshkaki00/4941-option-c/internal/llm"
)

func TestFakeStreamsReplyInOrder(t *testing.T) {
	f := &llm.Fake{Reply: "Full-time employees get 15 days [1]."}
	var pieces []string
	_, err := f.Generate(context.Background(), "sys", "usr", func(s string) error {
		pieces = append(pieces, s)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := joinPieces(pieces); got != f.Reply {
		t.Fatalf("streamed %q, want %q", got, f.Reply)
	}
	if len(pieces) < 2 {
		t.Fatalf("expected word-by-word streaming, got %d piece(s)", len(pieces))
	}
}

func TestFakeEmptyReplyEmitsNothing(t *testing.T) {
	f := &llm.Fake{}
	calls := 0
	usage, err := f.Generate(context.Background(), "s", "u", func(string) error { calls++; return nil })
	if err != nil || calls != 0 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
	if usage.OutputTokens != 0 {
		t.Fatalf("output tokens = %d for empty reply", usage.OutputTokens)
	}
}

func TestFakeReturnsScriptedError(t *testing.T) {
	boom := errors.New("upstream down")
	f := &llm.Fake{Reply: "never streamed", Err: boom}
	calls := 0
	_, err := f.Generate(context.Background(), "s", "u", func(string) error { calls++; return nil })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want scripted error", err)
	}
	if calls != 0 {
		t.Fatal("no text may stream when the scripted error is set")
	}
}

func TestFakeStopsWhenCallbackFails(t *testing.T) {
	stop := errors.New("client gone")
	f := &llm.Fake{Reply: "one two three four"}
	seen := 0
	_, err := f.Generate(context.Background(), "s", "u", func(string) error {
		seen++
		if seen == 2 {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) {
		t.Fatalf("err = %v, want callback error", err)
	}
	if seen != 2 {
		t.Fatalf("callback called %d times, want exactly 2 (stop immediately)", seen)
	}
}

func TestFakeHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &llm.Fake{Reply: "some text"}
	if _, err := f.Generate(ctx, "s", "u", func(string) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestFakeRecordsCallsAndCopiesThem(t *testing.T) {
	f := &llm.Fake{Reply: "ok"}
	_, _ = f.Generate(context.Background(), "sys-1", "usr-1", func(string) error { return nil })
	_, _ = f.Generate(context.Background(), "sys-2", "usr-2", func(string) error { return nil })

	calls := f.Calls()
	want := []llm.Call{{System: "sys-1", User: "usr-1"}, {System: "sys-2", User: "usr-2"}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %+v", calls)
	}
	calls[0].User = "mutated"
	if f.Calls()[0].User != "usr-1" {
		t.Fatal("Calls must return a copy, not internal state")
	}
}

func joinPieces(p []string) string {
	out := ""
	for _, s := range p {
		out += s
	}
	return out
}
