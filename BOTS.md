# BOTS.md

This document describes the bot system currently implemented in Pairline.

It covers:

- the Go control plane
- the Phoenix runtime
- the admin UI surface
- current bot behavior
- testing coverage
- AI token-efficiency characteristics

This file documents the code that exists today. It is not a design proposal.

## Overview

Pairline currently supports two bot families:

- `engagement` bots: deterministic scripted bots handled by Phoenix
- `ai` bots: model-backed bots handled by Phoenix through LangChain/OpenAI-compatible APIs

The implementation follows a split model:

- `backend/golang` owns bot definitions, bot settings, validation, role-protected admin APIs, and Redis snapshot publishing
- `backend/elixir/omegle_phoenix` owns live bot assignment, synthetic bot sessions, pairing, message delivery, disconnects, and runtime lifecycle
- `frontend/admin` exposes bot settings and bot definition management in the admin panel

## Code Map

Main files:

- Go control plane:
  - `backend/golang/internal/bots/http.go`
  - `backend/golang/internal/bots/models.go`
  - `backend/golang/internal/bots/snapshot.go`
  - `backend/golang/internal/bots/ai_config.go`
- Phoenix runtime:
  - `backend/elixir/omegle_phoenix/lib/omegle_phoenix/bots.ex`
  - `backend/elixir/omegle_phoenix/lib/omegle_phoenix/bots/script_worker.ex`
  - `backend/elixir/omegle_phoenix/lib/omegle_phoenix/bots/ai_worker.ex`
  - `backend/elixir/omegle_phoenix/lib/omegle_phoenix/bots/supervisor.ex`
- Admin routes:
  - `backend/golang/internal/server/server.go`
- Admin UI:
  - `frontend/admin/src/components/AdminPanelRuntime.tsx`

## Current Architecture

### 1. Go Control Plane

Go stores bot definitions in Postgres and bot runtime settings in `admin_settings`.

Definition fields currently include:

- `name`
- `slug`
- `bot_type`
- `is_active`
- `description`
- `match_modes_json`
- `bot_count`
- `traffic_weight`
- `targeting_json`
- `script_json`
- `ai_config_json`
- `message_limit`
- `session_ttl_seconds`
- `idle_timeout_seconds`
- `disconnect_reason`

Settings currently include:

- `bots.enabled`
- `bots.engagement.enabled`
- `bots.ai.enabled`
- `bots.match.rollout_percent`
- `bots.max_concurrent_runs`
- `bots.engagement.priority`
- `bots.ai.priority`
- `bots.emergency_stop`

Admin routes:

- read routes for `moderator`, `admin`, `root`
- write routes for `admin`, `root`

The route wiring lives in `backend/golang/internal/server/server.go`.

### 2. Redis Snapshot Publication

Whenever bot settings or definitions change, Go rebuilds a snapshot and publishes it to Redis.

Current snapshot keys:

- `bots:definitions:snapshot`
- `bots:settings:snapshot`

Current snapshot payload contains:

- `settings`
- `definitions`
- `updated_at`
- `updated_by_username`

Phoenix consumes `bots:definitions:snapshot` for runtime assignment.

### 3. Phoenix Runtime

Phoenix treats bots as synthetic sessions.

At runtime:

1. a human session becomes eligible for bot assignment
2. Phoenix loads the Redis snapshot
3. Phoenix filters matching bot definitions by:
   - global enablement
   - family enablement
   - rollout percent
   - chat mode
   - AI config completeness for AI bots
4. Phoenix prioritizes candidates by bot-family priority and weighted shuffle
5. Phoenix reserves capacity using Redis-backed active-run counters
6. Phoenix starts a worker under `OmeglePhoenix.Bots.Supervisor`
7. the worker creates a synthetic bot session and pairs it with the human session
8. messages and disconnects use the normal router/session pipeline

## Bot Selection Behavior

Selection logic lives in `backend/elixir/omegle_phoenix/lib/omegle_phoenix/bots.ex`.

Important runtime rules:

- bot sessions are never re-botted; `session_kind == :bot` is skipped immediately
- rollout uses a deterministic hash of session ID
- family-level priority is controlled by:
  - `engagement_priority`
  - `ai_priority`
- within a priority tier, definitions are ordered using weighted random selection derived from `traffic_weight`
- capacity is enforced with:
  - per-definition active-run counters
  - a global active-run counter

Active-run counters live in Redis and are managed atomically with Lua scripts.

## Engagement Bots

Engagement bots use `OmeglePhoenix.Bots.ScriptWorker`.

Current behavior:

- create synthetic bot session
- pair with human
- send opening message if configured
- optionally emit typing indicators
- choose replies from:
  - `opening_messages`
  - `reply_messages`
  - `fallback_message`
  - `closing_message`
  - regex-based `triggers`
- stop when:
  - message limit is reached
  - idle timeout is reached
  - session TTL is reached
  - partner disconnects
  - router timeout happens

Script workers now include both:

- idle timeout timer
- total wall-clock TTL timer

These timers were recently fixed and now disconnect properly.

### Writing Regex For Engagement Bots

Engagement bot triggers are stored under `script_json.triggers`.

Each trigger entry looks like:

```json
{
  "regex": "(?i)hello|hi|hey",
  "reply": "Hey! Nice to meet you."
}
```

Current runtime behavior:

- triggers are evaluated in Phoenix by `ScriptWorker`
- triggers are checked in list order
- the first matching trigger wins
- if no trigger matches, the worker falls back to `reply_messages`
- regex compilation errors are ignored silently for that trigger

That means authoring should favor simple, valid, first-match rules.

#### Regex Engine Notes

Regexes are compiled in Elixir with `Regex.compile/2`.

Current implementation also passes the `"i"` option at compile time, so matching is already case-insensitive even if you omit `(?i)`.

Practical implication:

- `hello`
- `(?i)hello`

will both behave case-insensitively today

Even so, keeping `(?i)` in patterns can still make intent clearer for admins reading the config.

#### Recommended Patterns

Use simple keyword-style matching for most flows.

Examples:

- greeting detection:
  - `(?i)\b(hi|hello|hey)\b`
- yes/affirmative:
  - `(?i)\b(yes|yeah|yep|sure|ok|okay)\b`
- no/negative:
  - `(?i)\b(no|nope|nah)\b`
- age mention:
  - `(?i)\b(i am|i'm)\s+\d{1,2}\b`
- laughter:
  - `(?i)\b(lol|lmao|haha+|hehe+)\b`

Using `\b` word boundaries is usually better than loose substring matching because it reduces accidental matches inside longer words.

#### Good Authoring Guidelines

- prefer short, targeted patterns over large “catch-all” expressions
- use alternation for small synonym sets:
  - `\b(cat|dog|pet|animal)\b`
- anchor when you only want exact or near-exact replies:
  - `(?i)^\s*(yes|no)\s*$`
- use `\s+` where users may type variable spacing
- keep patterns readable enough that non-engineers can still maintain them later

#### Handling Spaces

Spaces matter in regex patterns.

If you write a literal space, it matches exactly one space character.

Example:

- `how are you`

matches:

- `how are you`

but may not match:

- `how   are   you`
- `how are you `
- ` how are you`

If users may type extra spaces, tabs, or uneven spacing, prefer `\s+` between words and optional surrounding whitespace.

Examples:

- exact phrase with flexible internal spacing:
  - `(?i)how\s+are\s+you`
- exact phrase with flexible spacing at both ends:
  - `(?i)^\s*how\s+are\s+you\s*$`

If you want to match one or more words separated by spaces, avoid hardcoding many literal spaces unless the input format is tightly controlled.

#### Things To Avoid

- avoid extremely broad patterns like `.*`
- avoid patterns that match almost everything
- avoid overlapping trigger rules unless the order is intentional
- avoid overly complex nested groups when a few simpler triggers would be clearer
- avoid relying on invalid regex syntax; invalid triggers currently fail closed and just never match

#### Ordering Matters

Because the first matching trigger wins, put the most specific rules first and broader rules later.

Example:

Bad ordering:

```json
[
  { "regex": "(?i)\b(hi|hello|hey|help)\b", "reply": "Hi there!" },
  { "regex": "(?i)\bhelp\b", "reply": "What do you need help with?" }
]
```

Here, `help` usually matches the first rule and never reaches the more specific one.

Better ordering:

```json
[
  { "regex": "(?i)\bhelp\b", "reply": "What do you need help with?" },
  { "regex": "(?i)\b(hi|hello|hey)\b", "reply": "Hi there!" }
]
```

#### Safe Testing Advice

When writing trigger regexes:

- test a few expected inputs
- test a few near-misses
- test mixed case
- test extra whitespace
- test common slang or abbreviations if they matter for the flow

If a rule is business-critical, prefer multiple small explicit triggers over one dense regex.

## AI Bots

AI bots use `OmeglePhoenix.Bots.AIWorker`.

Current behavior:

- create synthetic bot session
- pair with human
- optionally send an opening request to the model
- keep short in-memory conversation history
- queue user messages while a model call is in flight
- call an OpenAI-compatible endpoint through LangChain
- send typing indicators while generation is in progress
- disconnect on:
  - message limit
  - idle timeout
  - session TTL
  - partner disconnect
  - router timeout
  - generation failure fallback completion

The worker does not persist transcripts to Postgres.

## Admin UI

The admin UI currently supports:

- reading bot settings
- editing bot settings
- listing definitions
- creating definitions
- updating definitions
- activating/deactivating definitions
- deleting definitions

The runtime settings area now uses a modal in `frontend/admin/src/components/AdminPanelRuntime.tsx` for:

- rollout percent
- max concurrent runs
- bot selection mode
- engagement/AI priority values

Bot definitions are still edited in the larger bot builder modal.

## Current Match Semantics

Bots are implemented as matched sessions rather than as a separate transport.

That means:

- pairing still goes through `SessionManager.pair_sessions/2`
- disconnects still go through router notifications
- bot sessions still have Redis-backed session state
- client-visible partner metadata still flows through normal match notifications

For bot-human matches, Phoenix sends partner metadata including:

- `session_kind`
- `bot_type`
- `reportable`
- `video_enabled`

## Reporting and Moderation Notes

The broader bot/report handling spans more than the bot modules themselves.

At a high level:

- the client can still surface report affordances for bot chats
- backend moderation logic can distinguish bot sessions
- bot sessions also preserve short-lived report-kind metadata in Redis during cleanup flows

## AI Token Efficiency Audit

### Short Answer

The AI bot backend is bounded, but it is not especially token-efficient.

### What Is Good

- Conversation length is capped by `message_limit`
- Output size can be capped with `ai_config_json.max_tokens`
- Sessions have both idle and wall-clock limits
- No transcript persistence means there is no long-lived context bloat
- The implementation is simple and predictable for short-run chats

### What Is Inefficient

1. Full history is resent on every generation

`AIWorker.generate_reply/3` rebuilds the model input as:

- full system prompt
- full in-memory conversation history
- current request

That means token usage grows turn by turn instead of staying near-constant.

2. There is no sliding context window

The worker keeps the whole `history` list in memory for the life of the bot run.
There is no:

- truncation to the last N exchanges
- token-budget trimming
- summary compression

3. A fresh model client/chain is built per request

`ChatOpenAI.new!/1`, `LLMChain.new!/1`, and `LLMChain.add_messages/2` are rebuilt for each call.
This is not the main token cost, but it is extra per-request overhead.

4. Queued user messages still preserve earlier full history

The queueing behavior is correct for ordering, but when the worker processes the next queued request it still sends the full accumulated history again.

### Current Practical Impact

For the current implementation:

- default AI conversations are short
- `message_limit` is capped
- bot runs are ephemeral rather than long-lived

The main backend token cost comes from repeated full-history replay during model calls.

## Test Coverage

Current relevant tests include:

- `backend/elixir/omegle_phoenix/test/bots_test.exs`
  - slot reservation/release behavior
  - weighted prioritization behavior
  - scripted timeout fallback disconnect regression
- `backend/elixir/omegle_phoenix/test/redis_live_integration_test.exs`
  - live scripted worker idle-timeout disconnect behavior
