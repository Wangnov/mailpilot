# mailpilot-go

> Minimal, push-style AI email assistant — in **a single Go binary**. New mail → LLM → your phone. Self-hosted, **zero runtime dependencies**.

A Go port of [mailpilot](https://github.com/Wangnov/mailpilot) (the Python version). Same pipeline, packaged as one static binary you `scp` and run — **no Python / pip / venv on the target host**. Uses goroutine-based IMAP IDLE for second-level latency.

## Why the Go version

Cloudflare Workers can't run this (codex needs subprocesses; IMAP IDLE wants a long-lived process), so "no server" isn't on the table if you want the subscription-backed `codex` provider. The next best thing is making "needing a server" as cheap as possible: **one static binary, no dependencies, cross-compiled to any Linux/ARM/macOS**.

## Features

- 📦 **Single static binary** — `go build` → one file; `make cross` → linux/amd64, linux/arm64, darwin/arm64…
- ⚡ **Real-time** — IMAP IDLE (goroutine), seconds not polling
- 🧠 **Multi-provider with fallback** — `codex` (subscription) → `openai`/compatible → `ollama` (local)
- 🖼️ **Image emails OCR'd** — empty-body image mail → PaddleOCR before analysis
- 🔎 **Agentic history lookup** — `codex` can call `mailpilot tool-search` (a hidden subcommand of the same binary) to search related past mail
- 📱 **Smart multi-channel push** — Bark / Telegram / ntfy / Webhook; urgent→break-through+sound, spam→silent, codes→copyable, tap→open in Gmail, grouped by category
- ♻️ **Reliable** — dedup watermark + retry queue + first-run baseline + IDLE auto-reconnect
- 🔒 **Safe** — read-only IMAP, email body treated as untrusted, prompt-injection hardened

## Install

```bash
go install github.com/Wangnov/mailpilot-go@latest
# or from source:
git clone https://github.com/Wangnov/mailpilot-go && cd mailpilot-go && make build
# cross-compile a static binary for your server:
make cross           # → dist/mailpilot-linux-arm64, etc.
```

## Quick start

```bash
mailpilot init                 # writes config.yaml
$EDITOR config.yaml            # IMAP + one provider + one notifier
export IMAP_PASSWORD=...  OPENAI_API_KEY=...  BARK_KEY=...
mailpilot run                  # process once (cron-friendly)
mailpilot daemon               # stay resident on IMAP IDLE (real-time)
```

First run only records a watermark and does **not** push your existing backlog.

## Configuration

```yaml
imap:
  host: imap.gmail.com
  user: you@gmail.com
  password: ${IMAP_PASSWORD}     # Gmail: App Password
  force_ipv4: true               # set if your host's IPv6 egress is broken

analyze:
  providers:                     # tried in order; failure/rate-limit → next
    - type: codex
      model: gpt-5.3-codex-spark
    - type: openai
      model: gpt-4o-mini
      api_key: ${OPENAI_API_KEY}
    # - type: ollama
    #   model: qwen2.5
    #   base_url: http://localhost:11434

ocr:
  enabled: true
  token: ${PADDLEOCR_TOKEN}

notify:
  - type: bark
    key: ${BARK_KEY}
  # - type: telegram
  #   bot_token: ${TG_BOT_TOKEN}
  #   chat_id: ${TG_CHAT_ID}
  # - type: ntfy
  #   topic: my-mail
  # - type: webhook
  #   url: ${WEBHOOK_URL}        # 企业微信 / Slack / custom

pipeline:
  baseline_on_first_run: true
  history_search: true
```

## Deploy

```bash
scp dist/mailpilot-linux-arm64 server:/usr/local/bin/mailpilot   # one file, no deps
sudo cp deploy/mailpilot.service /etc/systemd/system/
sudo systemctl enable --now mailpilot
sudo journalctl -u mailpilot -f
```

## Providers

- **`codex`** — your ChatGPT subscription via the Codex CLI (the binary shells out to `codex exec`). Saves API spend but can be rate-limited — always put `openai`/`ollama` after it. It can agentically call `mailpilot tool-search` to pull related history.
- **`openai`** — OpenAI or any compatible endpoint (`base_url`), strict JSON-schema output. The reliable workhorse.
- **`ollama`** — fully local, private, zero-cost.

## vs the Python version

Same design, two implementations — pick by taste:

| | mailpilot-go | mailpilot (Python) |
|---|---|---|
| Deploy | one static binary, zero deps | needs python + pip deps |
| Concurrency | goroutines | imapclient + subprocess |
| Iterate | recompile | edit & run |

## License

MIT © 2026 wangnov
