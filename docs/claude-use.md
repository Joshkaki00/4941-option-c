# Claude use (this repo)

Quick reference for **Claude Code** (how we build) and the **Claude API** (how Ask the Handbook answers). Prefer live docs when versions drift.

| Surface | Canonical docs | Context7 library id |
|---|---|---|
| Claude Code | https://code.claude.com/docs/en/overview | `/anthropics/claude-code` |
| Claude API | https://platform.claude.com/docs/en/about-claude/pricing | `/websites/platform_claude_en` |
| Go Messages SDK | Anthropic / community Go SDK streaming guides | `/charmbracelet/anthropic-sdk-go` (Messages shape) |

## Claude Code (build agents)

```bash
# install (macOS)
curl -fsSL https://claude.ai/install.sh | bash
# or: brew install --cask claude-code

cd /path/to/4941-option-c
claude                          # interactive
claude -p "summarize spec.md"   # one-shot
claude --bg "run /verify"       # background
claude agents                   # parallel sessions
```

| Piece | Where in this repo |
|---|---|
| Project memory | `CLAUDE.md` |
| Shared settings + hooks | `.claude/settings.json` |
| Skills | `.claude/skills/{review,verify,handoff}/` → `/review` `/verify` `/handoff` |
| Hard hooks | `main-only`, `no-coauthor`, `require-read-before-write` (+ Cursor twin in `.cursor/hooks/`) |
| Spec / design | `spec.md`, `proposal.md` |

Also: https://code.claude.com/docs/en/cli-reference · [skills](https://code.claude.com/docs/en/skills) · [hooks](https://code.claude.com/docs/en/hooks-guide) · [agents](https://code.claude.com/docs/en/sub-agents)

## Claude API (RAG generation path)

Matches `proposal.md` §3 / §9: sync ask → stream tokens over SSE.

1. Put grounding + **"I don't know"** rules in **`system`**.
2. Put audience-filtered passages + user question in **`messages`**.
3. Call `POST /v1/messages` with **`stream: true`**.
4. Never send chunks the retrieval filter denied.

### curl (streaming)

```bash
curl https://api.anthropic.com/v1/messages \
  -H "anthropic-version: 2026-10-21" \
  -H "content-type: application/json" \
  -H "x-api-key: $ANTHROPIC_API_KEY" \
  -d '{
    "model": "claude-sonnet-5-5",
    "max_tokens": 1024,
    "stream": true,
    "system": "Answer ONLY from the passages. If unsupported, say I do not know.",
    "messages": [{"role":"user","content":"<passages>\n\nQuestion: ..."}]
  }'
```

### Go streaming sketch (generation goroutine → channel → SSE)

```go
stream := client.Messages.NewStreaming(ctx, anthropic.MessageNewParams{
	MaxTokens: 1024,
	Model:     anthropic.ModelClaudeSonnet5, // pin a concrete ID in code
	System:    []anthropic.TextBlockParam{{Text: groundingRules}},
	Messages: []anthropic.MessageParam{
		anthropic.NewUserMessage(anthropic.NewTextBlock(userPrompt)),
	},
})
message := anthropic.Message{}
for stream.Next() {
	ev := stream.Current()
	_ = message.Accumulate(ev)
	// forward text deltas on the request context; cancel on client disconnect
}
if err := stream.Err(); err != nil {
	return err
}
```

### Cost anchors (verify before budgeting)

From proposal §9.1 (Haiku 5.5 / Sonnet 5.5 list, ~4k in / ~300 out):

| Model | Approx $/question |
|---|---|
| Haiku 5.5 | ~$0.00055 |
| Sonnet 5.5 | ~$0.011 |

Pricing: https://platform.claude.com/docs/en/about-claude/pricing

## Refresh

When models or CLI flags change: re-query Context7 (`/anthropics/claude-code`, `/websites/platform_claude_en`) or fetch the URLs above; update this file and `proposal.md` §9 in the same change.
