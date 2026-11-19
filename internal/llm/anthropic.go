package llm

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// Anthropic streams Messages API responses (primary LLM, proposal.md §9).
type Anthropic struct {
	client    anthropic.Client
	model     anthropic.Model
	maxTokens int64
}

// NewAnthropic builds a client. The API key is read by the caller, not hard-coded.
func NewAnthropic(apiKey, model string, maxTokens int64) (*Anthropic, error) {
	if apiKey == "" {
		return nil, ErrNoAPIKey
	}
	return &Anthropic{
		client:    anthropic.NewClient(option.WithAPIKey(apiKey)),
		model:     anthropic.Model(model),
		maxTokens: maxTokens,
	}, nil
}

// Generate implements Generator. Text deltas go to onText as they arrive, and the
// request context cancels the upstream call when the client disconnects.
func (a *Anthropic) Generate(ctx context.Context, system, user string, onText func(string) error) (Usage, error) {
	stream := a.client.Messages.NewStreaming(ctx, anthropic.MessageNewParams{
		Model:     a.model,
		MaxTokens: a.maxTokens,
		System:    []anthropic.TextBlockParam{{Text: system}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(user)),
		},
	})

	var msg anthropic.Message
	for stream.Next() {
		ev := stream.Current()
		if err := msg.Accumulate(ev); err != nil {
			return Usage{}, fmt.Errorf("accumulate stream: %w", err)
		}
		if text, ok := textDelta(ev); ok {
			if err := onText(text); err != nil {
				return Usage{}, err
			}
		}
	}
	if err := stream.Err(); err != nil {
		return Usage{}, fmt.Errorf("anthropic stream: %w", err)
	}
	return Usage{
		InputTokens:  int(msg.Usage.InputTokens),
		OutputTokens: int(msg.Usage.OutputTokens),
	}, nil
}

// textDelta returns the text of a content_block_delta text event, or false for any other event.
func textDelta(ev anthropic.MessageStreamEventUnion) (string, bool) {
	delta, ok := ev.AsAny().(anthropic.ContentBlockDeltaEvent)
	if !ok {
		return "", false
	}
	text, ok := delta.Delta.AsAny().(anthropic.TextDelta)
	if !ok {
		return "", false
	}
	return text.Text, true
}
