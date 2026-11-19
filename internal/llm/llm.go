// Package llm defines the generation interface and its implementations.
package llm

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Usage is token accounting for one generation.
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// Generator streams a grounded answer. onText receives each text delta in order.
// If onText returns an error, generation stops and that error is returned.
type Generator interface {
	Generate(ctx context.Context, system, user string, onText func(string) error) (Usage, error)
}

// Fake is a scripted Generator for tests and evals without network access.
type Fake struct {
	Reply string
	Err   error

	mu    sync.Mutex
	calls []Call
}

// Call records one invocation of Fake.Generate.
type Call struct {
	System string
	User   string
}

// Generate implements Generator by streaming Reply word by word.
func (f *Fake) Generate(ctx context.Context, system, user string, onText func(string) error) (Usage, error) {
	f.mu.Lock()
	f.calls = append(f.calls, Call{System: system, User: user})
	f.mu.Unlock()

	if f.Err != nil {
		return Usage{}, f.Err
	}
	for _, piece := range strings.SplitAfter(f.Reply, " ") {
		if piece == "" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return Usage{}, err
		}
		if err := onText(piece); err != nil {
			return Usage{}, err
		}
	}
	return Usage{InputTokens: len(system+user) / 4, OutputTokens: len(f.Reply) / 4}, nil
}

// Calls returns a copy of the recorded invocations.
func (f *Fake) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Call(nil), f.calls...)
}

// ErrNoAPIKey is returned when the hosted provider is selected without a key.
var ErrNoAPIKey = fmt.Errorf("llm: ANTHROPIC_API_KEY is not set")
