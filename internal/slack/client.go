package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client is the subset of the Slack Web API the bot uses.
type Client interface {
	PostMessage(ctx context.Context, channel, threadTS, text string) (string, error)
	UpdateMessage(ctx context.Context, channel, ts, text string) error
}

// APIClient calls the Slack Web API with a bot token. The token is never logged.
type APIClient struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// NewAPIClient returns a client for slack.com with a bounded HTTP timeout.
func NewAPIClient(token string) *APIClient {
	return &APIClient{
		BaseURL: "https://slack.com/api",
		Token:   token,
		HTTP:    &http.Client{Timeout: 10 * time.Second},
	}
}

// PostMessage posts text and returns the new message timestamp. An empty threadTS posts top-level.
func (c *APIClient) PostMessage(ctx context.Context, channel, threadTS, text string) (string, error) {
	payload := map[string]string{"channel": channel, "text": text}
	if threadTS != "" {
		payload["thread_ts"] = threadTS
	}
	out, err := c.call(ctx, "chat.postMessage", payload)
	if err != nil {
		return "", err
	}
	return out.TS, nil
}

// UpdateMessage replaces the text of an existing message.
func (c *APIClient) UpdateMessage(ctx context.Context, channel, ts, text string) error {
	_, err := c.call(ctx, "chat.update", map[string]string{"channel": channel, "ts": ts, "text": text})
	return err
}

type slackReply struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
	TS    string `json:"ts"`
}

// call posts a JSON request to a Slack Web API method and returns the reply. A reply with ok=false becomes an error that names Slack's error code.
func (c *APIClient) call(ctx context.Context, method string, payload map[string]string) (slackReply, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return slackReply{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/"+method, bytes.NewReader(body))
	if err != nil {
		return slackReply{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return slackReply{}, fmt.Errorf("slack %s: %w", method, err)
	}
	defer resp.Body.Close()
	var out slackReply
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return slackReply{}, fmt.Errorf("slack %s: decode: %w", method, err)
	}
	if !out.OK {
		return slackReply{}, fmt.Errorf("slack %s: %s", method, out.Error)
	}
	return out, nil
}
