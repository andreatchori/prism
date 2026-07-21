# Prism

> Self-hosted AI code review agent with fully customizable rules - your model, your infrastructure, zero data leaks.

![License](https://img.shields.io/badge/license-MIT-blue.svg)
![Go](https://img.shields.io/badge/go-1.22+-00ADD8.svg)
![Rust](https://img.shields.io/badge/rust-1.76+-orange.svg)
![Status](https://img.shields.io/badge/status-WIP-yellow.svg)

## Why choose Prism

### The fundamental problem with existing tools

```
CodeRabbit, PR-Agent, Kodus...
        |
        v
Your code → Internet → Their servers → OpenAI/Anthropic

        x  Proprietary code travels over the internet
        x  You pay per token / user / month
        x  You depend on THEIR uptime
        x  Their rules, not yours
        x  Often Python - limited concurrency
        x  Reviews tend to queue one after another
```

### What Prism changes - 5 decisive advantages

#### 1. Your code never leaves

```
With the others:
Dev → PR → Vendor servers → OpenAI → Comment
                    ^
              YOUR CODE HERE

With Prism:
Dev → PR → YOUR VPS → Ollama (local) → Comment
              ^
        EVERYTHING STAYS HERE
```

For a bank, a fintech, a healthtech, or any startup with sensitive IP -
**that is non-negotiable**.

#### 2. Go + Rust engine - built for concurrency

```
                  Startup      Idle RAM    100 concurrent PRs
                  ───────      ────────    ──────────────────
Python / PR-Agent  3-5s         ~150MB      Queue under load
Go+Rust / Prism    ~50ms        ~12MB       Parallel goroutines
```

**Go** orchestrates everything in parallel:

```
50 PRs arrive at once?
→ 50 goroutines started immediately
→ Each reviewed independently
→ No artificial queue
→ Webhook acknowledged in milliseconds (202 Accepted)
```

**Rust** analyzes at near-native speed:

```
10,000-line diff:
→ Diff parser        :  ~2ms
→ Rules engine       :  ~5ms   (your rules, evaluated deterministically)
→ Security patterns  :  ~1ms   (secrets / forbidden patterns)
→ Total without LLM  :  ~10ms  - instant
→ Total with Ollama  :  depends on the model (seconds to tens of seconds)
```

#### 3. Works even without AI

```
PR-Agent fails if the LLM is down        →  no review
SaaS reviewers fail if their cloud is down →  no review

Prism                                    →  Rust engine always runs
                                            deterministic reviews still post
                                            Ollama is a bonus, not a dependency
```

Full flow:

```
PR opened
    |
    v
Go receives webhook              (~1ms)
    |
    |--▶ Rust parser             (~2ms, always on)
    |--▶ Rust rules engine       (~5ms, always on)
    |--▶ Rust security checks    (~1ms, always on)
    |
    └--▶ Ollama / OpenAI / Anthropic / Azure OpenAI  (if available)
              |
              v
         Go merges findings + LLM body and posts the comment
```

#### 4. Your rules = your team culture

```
CodeRabbit / PR-Agent  →  "No unused variables"   (generic)

Prism                  →  "Every domain function must have
                            an associated integration test"

                       →  "HTTP handlers always end with Handler"

                       →  "No merge without a CHANGELOG update"
```

That is the difference between a generic tool and **a member of your team**.

#### 5. Real cost over 1 year

```
                  10 devs       50 devs       100 devs
                  ───────       ───────       ────────
CodeRabbit        ~$1,440/yr    ~$7,200/yr    ~$14,400/yr
PR-Agent Pro      ~$900/yr      ~$4,500/yr    ~$9,000/yr
Kodus             ~$1,200/yr    ~$6,000/yr    ~$12,000/yr

Prism             $0            $0            $0
(just the VPS / GPU you already run)
```

Prices above are indicative - check each vendor for current plans.
Prism itself is free software; you only pay for your own infra (and optional API tokens
if you choose OpenAI / Anthropic / Azure OpenAI instead of local Ollama).

### Full comparison

| | **Prism** | PR-Agent | CodeRabbit | SonarQube |
|---|---|---|---|---|
| Code stays on your infra | Yes | Optional / limited | No (vendor cloud) | Yes (self-hosted) |
| Privacy by default | Yes | Partial | No | Yes |
| Reliable local Ollama | Yes | Weak / fragile | No | No |
| Works without LLM | Yes (Rust engine) | No | No | Yes (SAST) |
| 100% custom rules | Yes (`rules.toml`) | Partial | Limited | SAST-oriented |
| Multi-platform | GitHub, GitLab, Azure, Bitbucket | Broad | Mostly GitHub | Broad |
| Stack | Go + Rust | Python | SaaS | Java |
| High PR concurrency | Yes (goroutines) | Limited | Cloud-scaled | Limited |
| Install | Minutes (binary / Compose) | Heavier | SaaS | Heavy |
| Price | Free | Free / paid | Paid per user | Free / paid |
| Open source | Yes | Yes | No | Partial |

### In one sentence

> Prism is the code reviewer where your code never leaves your infra, your rules
> reflect your team culture, the Rust engine answers in milliseconds, Go runs many
> reviews in parallel - all on a VPS you already own, without paying a reviewer SaaS.

## Architecture

```
Webhook (GitHub / GitLab / Azure / Bitbucket)
        ↓
  Go Core (orchestration)
        ↓
  Rust Engine (diff parsing + rules)
        ↓
  Ollama (local LLM)
        ↓
  Comment posted on PR
```

## Tech Stack

| Layer | Language | Role |
|---|---|---|
| Webhook server | Go | Receives events from platforms |
| Orchestration | Go | Coordinates the review flow |
| Diff parser | Rust | Parses PR diffs fast |
| Rules engine | Rust → WASM | Evaluates your custom rules |
| CLI tool | Rust | Local check before git push |
| LLM | Ollama / OpenAI / Anthropic | Runs the AI model (local by default) |

## How it works

1. A developer opens a Pull Request
2. The platform sends a webhook to your Prism server
3. Prism fetches the diff and runs it through your rules
4. Ollama analyzes the code based on your configuration
5. Prism posts a structured review comment on the PR
6. PR is approved or blocked based on the results

## Configuration

Define your own rules in `rules.toml` :

```toml
[reviewer]
name = "Prism Bot"
language = "en"

[rules.forbidden]
items = [
    "No hardcoded secrets or API keys",
    "No unwrap() without justification",
    "No debug print statements in production",
]

[rules.must_have]
items = [
    "Every function must have a comment",
    "Errors must always be handled",
]

[behavior]
block_on_critical = true
suggest_fixes = true
praise_good_code = true
# Opt-in: post one-click applicable code suggestions based on the rules above.
propose_changes = true
```

### Choosing an LLM provider

By default Prism uses a **local Ollama** model - no API key, no cost, code never
leaves your infrastructure. You can instead point Prism at a hosted API:

```toml
[llm]
provider = "anthropic"   # ollama | openai | anthropic | azure-openai
model = "claude-3-5-sonnet-latest"
fallback = "ollama"      # optional: used if the primary provider fails
```

API keys are read from the environment only (never from `rules.toml`):
`OPENAI_API_KEY` for OpenAI, `ANTHROPIC_API_KEY` for Anthropic, and
`AZURE_OPENAI_API_KEY` + `AZURE_OPENAI_ENDPOINT` + `AZURE_OPENAI_DEPLOYMENT` for
Azure OpenAI (there, `model` is the deployment name). `PRISM_LLM_PROVIDER` and
`PRISM_LLM_FALLBACK` override the config values.

With `fallback` set, Prism tries the primary provider first and transparently
retries on the secondary when the primary fails (or can't be configured) - handy
for "cloud first, local Ollama as backup".

> Note: this requires a paid **API** plan (platform.openai.com / console.anthropic.com).
> A consumer ChatGPT or Claude subscription does **not** grant API access.
> Sending private code to a hosted provider is a compliance decision - Ollama keeps
> everything local.

### Rule-based suggestions (`propose_changes`)

When `propose_changes = true`, Prism does not only flag issues - it proposes a
concrete fix tied to the rules the manager defined. On GitHub these are posted as
native **suggested changes**, so the PR author can apply them in one click. The
model returns each proposal in a machine-readable block (file, line range, and
replacement code); Prism strips that block from the human-readable comment and
renders it as inline ```suggestion blocks. Leave the flag `false` to keep Prism
in report-only mode.

Supported platforms for one-click suggestions:

- **GitHub** - native suggested changes (```suggestion) posted as an inline review.
- **GitLab** - inline discussions using the ```suggestion:-0+N syntax (anchored
  via the MR diff refs); multi-line proposals are supported. On subsequent pushes,
  existing Prism suggestions at the same file/line are updated in place rather
  than duplicated, and stale ones (no longer proposed) are automatically resolved.

- **Azure DevOps** & **Bitbucket** - inline comments with a copyable code block
  (no native one-click apply); existing Prism suggestions at the same file/line
  are skipped to avoid duplicates.

Reliability: outbound API calls retry transient failures (HTTP 429/5xx) honoring
`Retry-After`, and duplicate webhook deliveries are ignored via the provider's
delivery id. Logging is structured (`PRISM_LOG_FORMAT=text|json`, `PRISM_LOG_LEVEL`).

## Getting Started

### Requirements

- Go 1.22+
- [Ollama](https://ollama.com/) installed (or use Docker Compose below)
- A GitHub and/or GitLab token for posting reviews

### Quick start (local)

```bash
# 1. Pull a model
ollama pull llama3.2

# 2. Configure
cp .env.example .env
# Edit .env: set GITHUB_TOKEN and/or GITLAB_TOKEN

# 3. Run Prism from the repo root
export $(grep -v '^#' .env | xargs)   # or set env vars manually on Windows
go run ./cmd/prism
```

Health check:

```bash
curl http://localhost:8080/health
```

Expose your machine (for webhooks):

```bash
ngrok http 8080
```

Point the webhook URL to `https://<ngrok-host>/webhook`.

### Docker Compose

```bash
cp .env.example .env
# Fill GITHUB_TOKEN / GITLAB_TOKEN (and optional webhook secrets)

docker compose up --build -d

# First time: pull a model into the Ollama container
docker compose exec ollama ollama pull llama3.2
```

Prism listens on port `8080`. Ollama is available at `http://localhost:11434`.

### Environment variables

| Variable | Default | Description |
|---|---|---|
| `PRISM_CONFIG` | `config/examples/rules.toml` | Path to rules file |
| `PRISM_PORT` | `8080` | HTTP port |
| `OLLAMA_URL` | `http://localhost:11434` | Ollama base URL |
| `OLLAMA_MODEL` | `deepseek-coder:6.7b` | Model name |
| `GITHUB_TOKEN` | - | GitHub PAT (read PR + write comments/status) |
| `GITHUB_WEBHOOK_SECRET` | - | Optional; enables signature verification |
| `GITLAB_TOKEN` | - | GitLab personal access token |
| `GITLAB_URL` | `https://gitlab.com` | GitLab instance URL |
| `GITLAB_WEBHOOK_SECRET` | - | Optional; must match webhook token |
| `AZURE_DEVOPS_ORG` | - | Azure DevOps organization |
| `AZURE_DEVOPS_PAT` | - | Azure DevOps personal access token |
| `AZURE_WEBHOOK_SECRET` | - | Optional; Basic auth password on the webhook |
| `BITBUCKET_USERNAME` | - | Bitbucket username (with app password) |
| `BITBUCKET_APP_PASSWORD` | - | Bitbucket app password |
| `BITBUCKET_TOKEN` | - | Optional OAuth/Bearer token (alternative auth) |
| `BITBUCKET_WEBHOOK_SECRET` | - | Optional; enables HMAC signature check |
| `PRISM_ENGINE` | auto-detect | Path to Rust `prism` CLI (`check --json`); if unset, tries `rust/target/release/prism` |

### Webhooks

**GitHub:** Settings → Webhooks → URL `…/webhook`, content type `application/json`, event **Pull requests**. Optional secret = `GITHUB_WEBHOOK_SECRET`.

**GitLab:** Settings → Webhooks → URL `…/webhook`, trigger **Merge request events**. Secret token = `GITLAB_WEBHOOK_SECRET`.

**Azure DevOps:** Project Settings → Service hooks → Web Hooks → events **Pull request created** / **updated**. URL `…/webhook`. Optional Basic auth password = `AZURE_WEBHOOK_SECRET`. Azure has no cheap raw diff, so Prism fetches changed file contents (capped at 50 files / 200 KB each) and reviews them as added lines.

**Bitbucket:** Repository Settings → Webhooks → URL `…/webhook`, triggers **Pull Request Created** / **Updated**. Optional secret = `BITBUCKET_WEBHOOK_SECRET`.

Prism responds with `202 Accepted` immediately and runs the review in the background (Ollama can take minutes).

On GitHub, Prism posts a single summary comment (updated in place on new pushes) and, when the Rust engine is enabled, adds inline review comments on the exact lines of deterministic findings.

### Rust CLI (local check)

Deterministic rules run locally before you push (no Ollama required):

```bash
cd rust
cargo build --release
./target/release/prism check --config ../config/examples/rules.toml
# or pipe a diff:
git diff | ./target/release/prism check --stdin --config ../config/examples/rules.toml
```

Exit code `1` if `block_on_critical` is enabled and critical findings exist.

The Go server also invokes this CLI (when available) **before** Ollama:

1. Deterministic Rust rules on the diff (`prism check --stdin --json`)
2. Ollama review, with those findings injected into the prompt
3. Combined PR comment (engine section + LLM section)

Build the engine for the server:

```bash
cd rust && cargo build --release
# optional explicit path:
export PRISM_ENGINE="$(pwd)/target/release/prism"   # .exe on Windows
```

## Contributing

Contributions are welcome! This project is looking for maintainers.

1. Fork the repo
2. Create your branch (`git checkout -b feature/my-feature`)
3. Commit your changes (`git commit -m 'feat: add my feature'`)
4. Push to the branch (`git push origin feature/my-feature`)
5. Open a Pull Request

## License

MIT - see [LICENSE](./LICENSE)
