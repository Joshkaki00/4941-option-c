package slack

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Joshkaki00/4941-option-c/internal/answer"
	"github.com/Joshkaki00/4941-option-c/internal/auth"
	"github.com/Joshkaki00/4941-option-c/internal/store"
)

const (
	busyPostWait = 2 * time.Second // keeps the busy reply quick; the event is already acknowledged
	finalWait    = 10 * time.Second
)

// Config tunes the bot. Zero values get defaults in NewBot.
type Config struct {
	BotUserID  string        // the bot's own user ID, used to strip mentions (from auth.test)
	Workers    int           // bounded ask concurrency (default 2)
	QueueSize  int           // pending questions before "busy" replies (default 16)
	AskTimeout time.Duration // per-question generation budget (default 30s)
}

// Job is one question waiting for an answer.
type Job struct {
	Channel  string
	ThreadTS string
	User     string
	Question string
}

// Store is the persistence surface for Slack answers. *store.Store satisfies it.
type Store interface {
	CreateConversation(ctx context.Context, userSub string) (string, error)
	InsertMessage(ctx context.Context, m store.MessageRow, retrieved []store.RetrievedRow) (string, error)
}

// Asker answers one question. *answer.Service satisfies it.
type Asker interface {
	Ask(ctx context.Context, req answer.Request, onToken func(string) error) (answer.Result, error)
}

// Bot answers Slack questions through a bounded worker pool.
type Bot struct {
	cfg    Config
	client Client
	store  Store
	asker  Asker
	log    *slog.Logger
	jobs   chan Job
}

// NewBot builds a bot. It does not start workers; call Run.
func NewBot(cfg Config, c Client, st Store, a Asker, log *slog.Logger) *Bot {
	if cfg.Workers <= 0 {
		cfg.Workers = 2
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 16
	}
	if cfg.AskTimeout <= 0 {
		cfg.AskTimeout = 30 * time.Second
	}
	if log == nil {
		log = slog.Default()
	}
	return &Bot{cfg: cfg, client: c, store: st, asker: a, log: log, jobs: make(chan Job, cfg.QueueSize)}
}

// AudiencesFor returns the audiences a Slack user may search. Slack identities carry no
// SSO groups, so v1 grants only the company-wide audience. Restricted documents stay hidden
// from Slack until an explicit Slack-to-SSO mapping exists.
func AudiencesFor(_ string) []string {
	return []string{auth.CompanyWideAudience}
}

// HandleEvent turns one event into a queued question, or replies "busy" when the queue is full.
// Events that aren't questions are ignored.
func (b *Bot) HandleEvent(ctx context.Context, ev InnerEvent) {
	q, ok := Question(ev, b.cfg.BotUserID)
	if !ok {
		return
	}
	thread := ev.ThreadTS
	if thread == "" {
		thread = ev.TS
	}
	job := Job{Channel: ev.Channel, ThreadTS: thread, User: ev.User, Question: q}
	if b.submit(job) {
		return
	}
	pctx, cancel := context.WithTimeout(ctx, busyPostWait)
	defer cancel()
	if _, err := b.client.PostMessage(pctx, job.Channel, job.ThreadTS, BusyText); err != nil {
		b.log.Error("slack busy reply failed", "channel", job.Channel, "err", err)
	}
}

// submit queues a job without blocking. It returns false when the queue is full.
func (b *Bot) submit(j Job) bool {
	select {
	case b.jobs <- j:
		return true
	default:
		return false
	}
}

// Run starts the worker pool and blocks until ctx is cancelled and every worker has returned.
func (b *Bot) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for i := 0; i < b.cfg.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.work(ctx)
		}()
	}
	wg.Wait()
}

func (b *Bot) work(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-b.jobs:
			b.process(ctx, j)
		}
	}
}

// process posts a placeholder, answers, then replaces the placeholder with the result.
// If the placeholder cannot be posted there is nothing to update, so no work is done.
func (b *Bot) process(ctx context.Context, j Job) {
	ts, err := b.client.PostMessage(ctx, j.Channel, j.ThreadTS, PlaceholderText)
	if err != nil {
		b.log.Error("slack placeholder failed", "channel", j.Channel, "err", err)
		return
	}

	askCtx, cancel := context.WithTimeout(ctx, b.cfg.AskTimeout)
	defer cancel()
	res, err := b.asker.Ask(askCtx, answer.Request{Question: j.Question, Audiences: AudiencesFor(j.User)}, nil)
	text := FailureText
	if err != nil {
		b.log.Error("slack ask failed", "channel", j.Channel, "err", err)
	} else {
		text = FormatAnswer(res)
		b.persist(ctx, j, res)
	}

	// The final update outlives a cancelled ctx so a shutdown does not leave "Looking…" behind.
	upCtx, upCancel := context.WithTimeout(context.WithoutCancel(ctx), finalWait)
	defer upCancel()
	if err := b.client.UpdateMessage(upCtx, j.Channel, ts, text); err != nil {
		b.log.Error("slack update failed", "channel", j.Channel, "err", err)
	}
}

func (b *Bot) persist(ctx context.Context, j Job, res answer.Result) {
	ctx = context.WithoutCancel(ctx)
	sub := "slack:" + j.User
	convID, err := b.store.CreateConversation(ctx, sub)
	if err != nil {
		b.log.Error("slack persist: conversation", "err", err)
		return
	}
	retrieved := make([]store.RetrievedRow, 0, len(res.Retrieved))
	for _, c := range res.Retrieved {
		retrieved = append(retrieved, store.RetrievedRow{
			VersionID: c.VersionID, ChunkID: c.ChunkID, Rank: c.Rank, Score: c.Score, UsedInAnswer: c.Used,
		})
	}
	_, err = b.store.InsertMessage(ctx, store.MessageRow{
		ConversationID: convID, UserSub: sub, Question: j.Question, Answer: res.Answer,
		Outcome: string(res.Outcome), Model: res.Model, LatencyMS: int(res.Latency.Milliseconds()),
		InputTokens: res.Usage.InputTokens, OutputTokens: res.Usage.OutputTokens,
	}, retrieved)
	if err != nil {
		b.log.Error("slack persist: message", "err", err)
	}
}
