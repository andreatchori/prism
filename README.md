# Prism

> Self-hosted AI code review agent with fully customizable rules — your model, your infrastructure, zero data leaks.

![License](https://img.shields.io/badge/license-MIT-blue.svg)
![Go](https://img.shields.io/badge/go-1.22+-00ADD8.svg)
![Rust](https://img.shields.io/badge/rust-1.76+-orange.svg)
![Status](https://img.shields.io/badge/status-WIP-yellow.svg)

## Why Prism?

- **100% self-hosted** — your code never leaves your infrastructure
- **Your rules** — define exactly what you want reviewed via a simple `rules.toml`
- **Local LLM** — powered by Ollama, no API keys, no cost
- **Multi-platform** — GitHub, GitLab, Azure DevOps, Bitbucket
- **Fast** — core engine written in Rust, orchestration in Go

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

> Work in progress — installation guide coming soon.

Requirements:
- Go 1.22+
- Rust 1.76+
- Ollama installed and running

## Contributing

Contributions are welcome! This project is looking for maintainers.

1. Fork the repo
2. Create your branch (`git checkout -b feature/my-feature`)
3. Commit your changes (`git commit -m 'feat: add my feature'`)
4. Push to the branch (`git push origin feature/my-feature`)
5. Open a Pull Request

## License

MIT — see [LICENSE](./LICENSE)
