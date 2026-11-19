# AI-Assisted Development: Best Practices, Consolidated

All "Best Practices" bullets from the nine lessons, merged into one reference and grouped by category. Each bullet is tagged with its source lesson. Where two lessons made the same point, the bullets are merged and both sources are listed.

**Source key**

| Tag | Lesson |
|---|---|
| Memory | Memory Is Everything |
| Ticket | Ticket to Merge |
| Coworkers | Thirteen Tiny Coworkers |
| Words | Words Before Code |
| Contact | Nothing Survives First Contact |
| House | House Rules |
| Observe | What Do You Even Do All Day? |
| Config | One Config to Rule Them All |
| Debug | Debugging the Debugger |

## Contents

1. [Context and Configuration](#1-context-and-configuration)
2. [Specifications and Planning](#2-specifications-and-planning)
3. [Testing and Verification](#3-testing-and-verification)
4. [Git, PRs, and the Development Loop](#4-git-prs-and-the-development-loop)
5. [Conventions, Linting, and Enforcement](#5-conventions-linting-and-enforcement)
6. [Parallel Agents and Subagents](#6-parallel-agents-and-subagents)
7. [Observability and Cost Management](#7-observability-and-cost-management)
8. [Debugging and Recovery](#8-debugging-and-recovery)
9. [Team Adoption and Rollout](#9-team-adoption-and-rollout)
10. [Course Use Cases](#10-course-use-cases)
11. [Codex Pro Tips](#11-codex-pro-tips)

## 1. Context and Configuration

- **Less is more.** Keep your root `CLAUDE.md` under 150 lines. Every instruction competes for the model's attention budget. If it doesn't apply to every session, it doesn't belong at the root. *(Memory)*
- **Pointers, not copies.** Don't paste code snippets into `CLAUDE.md`. They go stale. Reference file paths instead: "See `src/config/db.ts` for the database connection pattern." The agent reads the file when it needs to. *(Memory)*
- **Layer your context.** Global config for personal preferences, project root for repo-wide rules, subdirectory files for module-specific instructions. Each layer narrows the focus. *(Memory)*
- **Separate global from project.** Your personal preferences (`~/.claude/CLAUDE.md`) shouldn't conflict with project conventions (`./CLAUDE.md`). If they do, project wins. *(House)*
- **Share configs through the repo.** Check `.claude/` into version control. When a teammate clones the repo, they get the same commands, settings, and conventions. *(House)*
- **Version your conventions.** When team standards change, update `CLAUDE.md` in the same PR. The convention and the config evolve together. *(House)*
- **Custom commands save tokens.** A `.claude/commands/review.md` file that encodes your review checklist replaces a long prompt you'd type every time. *(Memory)*
- **Automate what you repeat.** If you do the same PR setup every time, encode it in a custom command. *(Ticket)*
- **Run `/init` on new repos.** It generates a starter `CLAUDE.md` by scanning your codebase. Not perfect, but better than a blank file. *(Memory)*
- **Audit regularly.** Every few weeks, check what instructions the agent actually follows. Remove what it ignores. Strengthen what matters. *(House)*
- **Cache what you can.** If the agent reads the same files every session, consider putting summaries in `CLAUDE.md` instead of making it re-read them. *(Observe)*

## 2. Specifications and Planning

- **Write the spec before you open the terminal.** The spec is the primary artifact. Code is the byproduct. If you can't describe what you want in plain English, the agent definitely can't build it. *(Words)*
- **Make every requirement testable.** "The API should be fast" is not testable. "The API should respond in under 200ms for the agreed percentile target" is testable. If you can't measure it, you can't verify it. *(Words)*
- **Use Given/When/Then.** It forces precision. "Given a user with no items in cart, when they click checkout, then show an empty cart message." No ambiguity. *(Words)*
- **Version your specs.** Start with v1.0 (MVP), then v1.1 (refinements), then v1.2 (stretch). This gives the agent clear phases to work through. *(Words)*
- **Include what you don't want.** Negative requirements prevent the agent from making assumptions. "Do NOT add authentication to this endpoint" saves a 20-minute rabbit hole. *(Words)*
- **Quality gates are checkpoints, not afterthoughts.** Define them upfront: "After each phase, run tests. If any fail, fix before proceeding." *(Words)*
- **One ticket, one branch, one PR.** Don't bundle unrelated changes. Agents generate cleaner code when the scope is narrow and well-defined. *(Ticket)*
- **Scope tasks narrowly.** Narrow tasks need less context and cost less. *(Observe, derived from the cost lesson's optimization guidance)*

## 3. Testing and Verification

- **Tests first, always.** Write the failing test before the implementation. Agents are excellent at making tests pass. They're terrible at knowing what to test. That's your job. *(Coworkers)*
- **Never trust, always verify.** AI-generated code can look perfect and be subtly wrong. It might call a library function that doesn't exist, use deprecated syntax, or handle errors in a way that silently swallows exceptions. *(Contact)*
- **Test the boundaries, not the middle.** Agents handle the happy path well. They miss edge cases: empty inputs, null values, extremely large data, concurrent access, timezone differences. *(Contact)*
- **Run the code.** Reading AI-generated code is not enough. Execute it. Watch what happens with real inputs. Many bugs only surface at runtime. *(Contact)*
- **Check external references.** If the agent uses a library you haven't seen, verify it exists and does what the agent claims. Hallucinated imports are one of the most common AI coding failures. *(Contact)*
- **Build a verification checklist.** After the agent finishes, run through: Do tests pass? Does the linter pass? Do imports resolve? Do API endpoints actually exist? Does the behavior match the spec? *(Contact)*
- **Trust but verify subagent output.** Subagents work in isolation. When they return, review what they did before merging. Parallel doesn't mean unsupervised. *(Coworkers)*

## 4. Git, PRs, and the Development Loop

- **Branch from main, merge to main.** Keep your branch strategy linear. The agent doesn't need to understand your team's complex branching philosophy if there's only one path. *(Ticket)*
- **Commit early, commit often.** Small commits make it easier to trace what the agent did and roll back if something goes wrong. A commit history should read like a story. *(Ticket)*
- **Let the agent write the first draft.** It's seen the full diff. It knows what changed. Edit for accuracy and tone, but don't start from scratch. *(Ticket)*
- **Review AI-generated PRs like you'd review a junior engineer's.** Don't rubber-stamp. Read the diff. Check edge cases. Verify tests actually test what they claim. *(Ticket)*

## 5. Conventions, Linting, and Enforcement

- **Let linters lint.** Don't waste `CLAUDE.md` instructions on formatting rules your linter already handles. Tabs vs. spaces, semicolons, line length: those belong in `.eslintrc` or `ruff.toml`, not in your AI config. *(House)*
- **Use hooks for hard rules.** If a rule is non-negotiable (no commits to main, no `console.log` in production code, always run tests before committing), use a PreToolUse or PostToolUse hook. Hooks enforce the rule. `CLAUDE.md` only advises the model. If something is critical, make it a hook. *(House, Memory)*

## 6. Parallel Agents and Subagents

- **One agent, one concern.** If a task touches multiple unrelated systems (frontend, backend, database), split it across subagents. Each one stays focused and produces cleaner output. *(Coworkers)*
- **Worktrees prevent chaos.** Every subagent should get its own git worktree. This gives it an isolated copy of the repo. No merge conflicts mid-task. *(Coworkers)*
- **Keep the orchestrator light.** The main session should decompose work and review results. Don't use it for heavy implementation. That's what subagents are for. *(Coworkers)*
- **Name your agents.** When spawning multiple subagents, give them descriptive names. "test-agent" and "api-agent" are easier to track than "agent-1" and "agent-2." *(Coworkers)*
- **Subagents on Haiku, orchestrator on Sonnet.** When running parallel agents, the subagents doing focused work can use the cheaper model. The main session doing decomposition and synthesis uses the smarter model. *(Observe)*

## 7. Observability and Cost Management

- **Watch before you optimize.** You can't reduce costs you don't understand. Run a few sessions with verbose logging before changing anything. *(Observe)*
- **Haiku is your default for routine work.** File reads, routine edits, formatting, boilerplate generation. Save Sonnet and Opus for tasks that actually need them. *(Observe)*
- **Set spending limits.** Configure a max budget per session or per day. One runaway premium-model loop can burn through real budget before you notice. *(Observe)*
- **Track token usage per task type.** Know what your common workflows cost. "Running tests" might be cheap. "Full codebase review" might be expensive. *(Observe)*

## 8. Debugging and Recovery

- **Read the error, not the vibes.** When Claude Code fails, it produces specific error messages. Read them. "Context window exceeded" is a different problem than "tool execution failed," and they have different fixes. *(Debug)*
- **Bisect the problem.** If a complex prompt fails, split it in half. Does the first half work? The second half? Find the smallest prompt that reproduces the failure. *(Debug)*
- **Check the obvious first.** API key expired? Internet down? Disk full? Git in a bad state? The most common failures aren't AI problems. They're environment problems. *(Debug)*
- **Don't retry the same thing.** If the agent failed with one approach, retrying the exact same prompt will produce the exact same failure. Change something: the prompt, the context, the model, or the scope. *(Debug)*
- **Keep escape hatches.** Always know how to stop a running agent (Ctrl+C), how to roll back changes (`git stash` or `git checkout`), and how to start a fresh session. *(Debug)*
- **Keep a failure log.** Every failure is data. Document each time the agent produces incorrect code or a session goes wrong, in your `CLAUDE.md` or a personal "gotcha" log. Patterns will emerge, and the most common ones belong in `CLAUDE.md` as warnings. *(Debug, Contact)*

## 9. Team Adoption and Rollout

- **Start with one team, one workflow.** Don't roll out to the entire organization at once. Pick one team, one use case (like PR review or test generation), and prove value before expanding. *(Config)*
- **Shared config first, individual adoption second.** If every developer has a different `CLAUDE.md`, the codebase gets inconsistent. Set up the shared `.claude/` directory before people start using the tool independently. *(Config)*
- **Measure before and after.** You need data: time to ship a feature, bugs caught in review, test coverage, developer satisfaction. Without metrics, "AI is helping" is just a feeling. *(Config)*
- **Address security concerns directly.** Teams worry about code leaving their environment, API key management, and accidental data exposure. Have answers for these before the first meeting. *(Config)*
- **Champions, not mandates.** Find 2-3 developers who are excited about AI tools. Let them demonstrate value. Peer enthusiasm is more persuasive than a top-down directive. *(Config)*
- **Budget for the learning curve.** The first two weeks will be slower, not faster. Developers are learning a new tool while doing their normal work. Plan for this. *(Config)*

## 10. Course Use Cases

The 💼 bullets from each lesson, tying the practice to specific course assignments.

- **Context** — Move repeated setup, test commands, and architecture hints into repo context when `fixthis` or `makeanything` keeps rereading the same files. *(Memory)*
- **Dev loop** — Run one-ticket, one-branch, one-PR loops in `firstbuild` and `fixthis` so agent output stays reviewable. *(Ticket)*
- **TDD and parallelism** — Use strict TDD for `firstbuild` features with fuzzy edge cases, then split `wiredup` into separate agent lanes when the work truly diverges. *(Coworkers)*
- **Specs** — Write `spec.md` first whenever a `firstbuild` task spans behavior, tests, and output formatting instead of one obvious file edit. *(Words)*
- **Verification** — Run the verification checklist before merging any `wiredup` or API-facing assignment code that touches external docs, packages, or services. *(Contact)*
- **Conventions** — Add shared commands, hooks, and conventions as soon as `fixthis` or `makeanything` starts involving repeated workflows or multiple contributors. *(House)*
- **Cost** — Compare one `firstbuild` session and one `makeanything` session to see how task size changes token patterns, tool calls, and model choice. *(Observe)*
- **Team defaults** — Use shared defaults whenever a team repo needs repeatable onboarding, review, and verification behavior across more than one contributor. *(Config)*
- **Failure logs** — Keep a failure log during `fixthis` and `wiredup` so repeated integration, environment, or context problems become explicit patterns. *(Debug)*

## 11. Codex Pro Tips

The 🛠️ bullets from each lesson, covering how the same discipline carries over to Codex.

- Repo instructions and verification notes work best when they stay short, current, and committed with the codebase. *(Memory)*
- Codex can summarize diffs and draft PR text too, but the human reviewer still decides whether the branch actually satisfies the issue. *(Ticket)*
- Parallel agent work scales best when each task owns a distinct write surface and a concrete verification command. *(Coworkers)*
- Scoped tasks improve when the spec names files in bounds, proof commands, and what the agent must not change. *(Words)*
- Codex-generated code needs the same external reference checks as Claude output; passing tests are evidence, not proof of correctness. *(Contact)*
- Cross-session consistency comes from repo rules plus automated checks, not from increasingly long style prompts. *(House)*
- Codex-optimized and smaller coding models reward the same habit as Claude tiers: classify the work before paying for deeper reasoning. *(Observe)*
- Adoption gets easier when permissions, instructions, and verification commands are centralized instead of personalized. *(Config)*
- Traces only become useful debugging assets when you keep the failing task, the evidence, and the final fix together. *(Debug)*
