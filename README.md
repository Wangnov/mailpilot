# mailpilot

> Minimal, push-style AI email assistant — in **a single Go binary**. New mail → LLM → your phone. Self-hosted, **zero runtime dependencies**.

`mailpilot` watches your inbox over **IMAP IDLE** and, the moment a new email arrives, runs it through an LLM (your ChatGPT subscription via `codex`, any OpenAI-compatible API, or a local Ollama model), then pushes a structured summary (category · urgency · one-line · key points) to your phone via **Bark / Telegram / ntfy / Webhook**.

One static binary you `scp` and run — **no Python / pip / venv on the target host**. goroutine-based IMAP IDLE for second-level latency.

## Why a single Go binary

Cloudflare Workers can't host this: `codex` needs subprocesses and IMAP IDLE wants a long-lived process — both unsupported on Workers/WASI. Running `codex` on Cloudflare would mean a paid Container (≈ renting a small VM), which defeats "serverless to save money". So if you want a server at all, make it as cheap as possible: **one dependency-free static binary, cross-compiled to any Linux/ARM/macOS**.

## Features

- 📦 **Single static binary** — `go build` → one file; `make cross` → linux/amd64·arm64, darwin/arm64…
- ⚡ **Real-time** — IMAP IDLE (goroutine), seconds not polling
- 🧠 **Multi-provider with fallback** — `codex` (subscription) → `openai`/compatible → `ollama` (local)
- 🔎 **Agentic history lookup, on every capable provider** — when a mail looks like a thread/issue reply, the model can autonomously search related past mail before answering. `codex` uses its own agent loop; `openai` uses a **built-in function-calling loop** (no LangChain, ~one file). So you don't lose history context when falling back off `codex`.
- 🖼️ **Image emails OCR'd** — empty-body image mail → PaddleOCR before analysis
- 📱 **Smart multi-channel push** — urgent→break-through+sound, spam→silent, codes→copyable, tap→open in Gmail, grouped by category
- ♻️ **Reliable** — dedup watermark + retry queue + first-run baseline + IDLE auto-reconnect
- 🔒 **Safe** — read-only IMAP, body treated as untrusted, prompt-injection hardened; `codex` runs confined to a throwaway project-local sandbox (`--ephemeral`, never touches your `~/.codex`)

## Install

```bash
go install github.com/Wangnov/mailpilot@latest
# or from source:
git clone https://github.com/Wangnov/mailpilot && cd mailpilot && make build
make cross           # static binaries for your server → dist/mailpilot-linux-arm64, etc.
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

- **`codex`** — your ChatGPT subscription via the Codex CLI (shells out to `codex exec`). Saves API spend but can be rate-limited — always put `openai`/`ollama` after it. Agentic via codex's own loop, calling `mailpilot tool-search`. Runs **confined**: a throwaway per-run sandbox under `<config-dir>/.mailpilot-work/` (auto-cleaned), `--ephemeral` so no session files pile up in `~/.codex`, and only ephemeral `-c`/`-m` overrides — it never edits your `~/.codex/config.toml` nor writes to system `/tmp` or your home dir (Linux & macOS alike).
- **`openai`** — OpenAI or any compatible endpoint (`base_url`). **Does agentic history search via a built-in function-calling loop**: the model can call `mail_search` over several rounds, then a final json-schema call produces strict structured output. The reliable workhorse.
- **`ollama`** — fully local, private, zero-cost. Single-shot (local models' tool-calling varies); use `openai`/`codex` for agentic history.

## How agentic history works (no framework)

`tool-search` is a hidden subcommand of the **same binary** — a read-only IMAP search/get/thread. `codex` calls it inside its sandbox; `openai`'s loop shells out to it too. So the one binary is simultaneously the daemon *and* the tool the LLM calls. No agent framework, no extra service.

## Security

Read-only IMAP (App Password, never sends/deletes); email bodies treated as **untrusted** (URLs stripped, wrapped, prompt forbids executing any in-body instructions). The `codex` subprocess is **confined to the project**: a fresh per-run sandbox dir (writes can't reach your `config.yaml`/`.env`), `--ephemeral` so nothing accumulates in `~/.codex`, your codex config left untouched, and zero footprint in system temp or your home dir. All credentials revocable.

## License

MIT © 2026 wangnov
