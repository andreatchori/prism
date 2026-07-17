# Prism

> Self-hosted AI code review agent with fully customizable rules - your model, your infrastructure, zero data leaks.

![License](https://img.shields.io/badge/license-MIT-blue.svg)
![Go](https://img.shields.io/badge/go-1.22+-00ADD8.svg)
![Rust](https://img.shields.io/badge/rust-1.76+-orange.svg)
![Status](https://img.shields.io/badge/status-WIP-yellow.svg)

## Why Prism?

- **100% self-hosted** - your code never leaves your infrastructure
- **Your rules** - define exactly what you want reviewed via a simple `rules.toml`
- **Local LLM** - powered by Ollama, no API keys, no cost
- **Multi-platform** - GitHub, GitLab, Azure DevOps, Bitbucket
- **Fast** - core engine written in Rust, orchestration in Go

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
| LLM | Ollama | Runs the AI model locally |

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
```

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

**Azure DevOps:** Project Settings → Service hooks → Web Hooks → events **Pull request created** / **updated**. URL `…/webhook`. Optional Basic auth password = `AZURE_WEBHOOK_SECRET`.

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
