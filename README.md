<p align="center">
  <img src="./assets/banner.svg" alt="mailpilot — new mail → LLM → your phone" width="100%">
</p>

<h1 align="center">mailpilot</h1>

<p align="center">
  <b>Push-style AI email assistant in a single Go binary.</b><br>
  New mail → LLM → your phone. Self-hosted, real-time, zero runtime dependencies.
</p>

<p align="center">
  <a href="https://github.com/Wangnov/mailpilot/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/Wangnov/mailpilot/ci.yml?branch=main&label=CI&logo=githubactions&logoColor=white" alt="CI"></a>
  <a href="https://github.com/Wangnov/mailpilot/releases/latest"><img src="https://img.shields.io/github/v/release/Wangnov/mailpilot?logo=github&label=release&color=4f46e5&sort=semver" alt="Release"></a>
  <a href="go.mod"><img src="https://img.shields.io/github/go-mod/go-version/Wangnov/mailpilot?logo=go&logoColor=white&label=Go" alt="Go version"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/Wangnov/mailpilot?color=2ea44f" alt="License"></a>
  <img src="https://img.shields.io/badge/platform-Linux%20·%20macOS-555?logo=linux&logoColor=white" alt="Platform">
  <img src="https://img.shields.io/badge/single%20binary-zero%20deps-38bdf8" alt="Single binary, zero deps">
  <img src="https://img.shields.io/badge/LLM-codex%20·%20openai%20·%20ollama-4f46e5" alt="Providers">
</p>

<p align="center">
  <a href="#readme-zh">中文</a> · <a href="#readme-en">English</a>
</p>

<p align="center">
  IMAP IDLE · agentic history · image OCR · Bark / Telegram / ntfy / Webhook · prompt-injection hardened · cross-compiled
</p>

---

<a id="readme-zh"></a>

## 🇨🇳 中文

`mailpilot` 盯着你的收件箱，通过 **IMAP IDLE** 在新邮件到达的那一刻，把它丢给一个 LLM（你的 ChatGPT 订阅经 `codex`、任意 OpenAI 兼容 API，或本地 Ollama），再把结构化摘要（**分类 · 紧急度 · 一句话 · 关键点**）经 **Bark / Telegram / ntfy / Webhook** 秒级推到你手机。

一个 `scp` 上去就能跑的静态二进制 —— 目标机 **不需要 Python / pip / venv**。goroutine 驱动的 IMAP IDLE 带来秒级延迟。

### 为什么是单个 Go 二进制

Cloudflare Workers 托不住它：`codex` 需要子进程、IMAP IDLE 需要常驻长连接 —— 两者在 Workers/WASI 上都不支持。在 CF 上跑 `codex` 只能上付费 Container（约等于租一台小 VM），那 “serverless 省钱” 就没意义了。所以如果总要有台机器，就让它尽量便宜：**一个零依赖静态二进制，交叉编译到任意 Linux / ARM / macOS**。

### ✨ 特性

- 📦 **单静态二进制** — `go build` 出一个文件；`make cross` 出 linux/amd64·arm64、darwin/arm64…
- ⚡ **实时** — IMAP IDLE（goroutine），秒级而非轮询
- 🧠 **多 provider 自动降级** — `codex`（订阅）→ `openai`/兼容端点 → `ollama`（本地）
- 🔎 **每个有能力的 provider 都能做 agentic 历史检索** — 当邮件像是某讨论串 / issue 的后续时，模型可自主先检索相关历史邮件再作答。`codex` 用它自己的 agent loop；`openai` 用**内置 function-calling 循环**（不挂 LangChain，约一个文件）。降级到 `openai` 也不丢历史上下文。
- 🖼️ **图片邮件 OCR** — 正文为空的纯图片邮件 → 先过 PaddleOCR 再分析
- 📱 **智能多渠道推送** — 紧急→破防+声音，垃圾/营销→静默，验证码→可复制，点按→在 Gmail 打开，按分类归组
- ♻️ **可靠** — 去重水位线 + 重试队列 + 首跑基线 + IDLE 断线自动重连
- 🔒 **安全** — 只读 IMAP，邮件正文视为不可信，prompt-injection 硬化；`codex` 被关进项目内一次性沙箱

### 🚀 快速开始

```bash
go install github.com/Wangnov/mailpilot@latest
# 或从源码构建：
git clone https://github.com/Wangnov/mailpilot && cd mailpilot && make build
make cross           # 给服务器出静态二进制 → dist/mailpilot-linux-arm64 等
```

```bash
mailpilot init                 # 生成 config.yaml
$EDITOR config.yaml            # IMAP + 一个 provider + 一个通知渠道
export IMAP_PASSWORD=...  OPENAI_API_KEY=...  BARK_KEY=...
mailpilot run                  # 处理一次（适合 cron 兜底）
mailpilot daemon               # 常驻 IMAP IDLE（实时）
```

> 首次运行只记录水位线，**不会**把你的存量邮件全推一遍。

### ⚙️ 配置

```yaml
imap:
  host: imap.gmail.com
  user: you@gmail.com
  password: ${IMAP_PASSWORD}     # Gmail：应用专用密码
  force_ipv4: true               # 本机 IPv6 出站不通时打开

analyze:
  providers:                     # 按序尝试；失败/限流 → 下一个
    - type: codex                # 你的 ChatGPT 订阅（需本机装 codex CLI）
      model: gpt-5.3-codex-spark
    - type: openai               # OpenAI 或任意兼容端点
      model: gpt-5.4-mini
      base_url: https://api.openai.com/v1
      api_key: ${OPENAI_API_KEY}
    # - type: ollama             # 全本地、隐私、零成本
    #   model: qwen2.5
    #   base_url: http://localhost:11434
  timeout: 300

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
  #   url: ${WEBHOOK_URL}        # 企业微信 / Slack / 自定义

pipeline:
  baseline_on_first_run: true
  history_search: true
```

`${ENV}` 在 YAML **解析后**对字符串字段展开，因此密钥里含 `: # "` 等特殊字符也不会破坏解析。

### 🧠 Provider 与降级

- **`codex`** — 你的 ChatGPT 订阅，经 Codex CLI（`codex exec`）。省 API 费但可能被限流，**永远把 `openai`/`ollama` 排在它后面**。agentic 走 codex 自己的 loop，调用 `mailpilot tool-search`。运行**被收进项目内一次性沙箱**：`--ephemeral`（不往 `~/.codex` 落 session）、临时产物只在 `<config 目录>/.mailpilot-work/` 且随用随清、只用临时 `-c`/`-m` 覆盖 —— 绝不改你的 `~/.codex/config.toml`，也不写系统 `/tmp` 或家目录（Linux / macOS 同此）。
- **`openai`** — OpenAI 或任意兼容端点（`base_url`）。**用内置 function-calling 循环做 agentic 历史检索**：模型多轮调用 `mail_search`，最后一次 json-schema 强约束出结构化结果。可靠的主力。
- **`ollama`** — 全本地、私有、零成本。单轮（本地模型工具调用能力参差）；要 agentic 历史用 `openai`/`codex`。

### 🔎 Agentic 历史检索（不挂框架）

`tool-search` 是**同一个二进制**的隐藏子命令 —— 只读 IMAP 的 search/get/thread。`codex` 在它的沙箱里调用它，`openai` 的循环也 shell 出去调它。于是这一个二进制**同时是 daemon、又是 LLM 调用的工具**。没有 agent 框架，没有额外服务。

### 📱 推送

按分析结果智能映射渠道能力：紧急→破防+声音、垃圾→静默、验证码→可复制、点按→在 Gmail 打开、按分类归组。开箱支持 **Bark / Telegram / ntfy / Webhook**（Webhook 兼容企业微信 / Slack 纯文本字段）。

### 🚢 部署

```bash
scp dist/mailpilot-linux-arm64 server:/usr/local/bin/mailpilot   # 一个文件，零依赖
sudo cp deploy/mailpilot.service /etc/systemd/system/
sudo systemctl enable --now mailpilot
sudo journalctl -u mailpilot -f
```

### 🔒 安全

只读 IMAP（应用专用密码，绝不发信/删信）；邮件正文视为**不可信**（URL 剥离、包裹、提示词禁止执行正文里的任何指令）。`codex` 子进程**被收进项目内**：一次性沙箱（写不到你的 `config.yaml`/`.env`）、`--ephemeral` 不在 `~/.codex` 堆积、不动你的 codex 配置、系统 temp / 家目录零足迹。所有凭据可吊销。

### 🛠 构建与发布

```bash
make build        # 当前平台
make cross        # linux/amd64·arm64 + darwin/arm64·amd64 → dist/
make test         # go test -race ./...
make vet
```

打 `v*` tag 即触发 GitHub Actions **Release** workflow：交叉编译四平台静态二进制、生成 `SHA256SUMS.txt`、发布 GitHub Release。

```bash
git tag v0.2.0 && git push origin v0.2.0
```

### 📄 License

MIT © 2026 wangnov

---

<a id="readme-en"></a>

## 🇬🇧 English

`mailpilot` watches your inbox over **IMAP IDLE** and, the moment a new email arrives, runs it through an LLM (your ChatGPT subscription via `codex`, any OpenAI-compatible API, or a local Ollama model), then pushes a structured summary (**category · urgency · one-line · key points**) to your phone via **Bark / Telegram / ntfy / Webhook**.

One static binary you `scp` and run — **no Python / pip / venv on the target host**. goroutine-based IMAP IDLE for second-level latency.

### Why a single Go binary

Cloudflare Workers can't host this: `codex` needs subprocesses and IMAP IDLE wants a long-lived process — both unsupported on Workers/WASI. Running `codex` on Cloudflare would mean a paid Container (≈ renting a small VM), which defeats "serverless to save money". So if you want a server at all, make it as cheap as possible: **one dependency-free static binary, cross-compiled to any Linux / ARM / macOS**.

### ✨ Features

- 📦 **Single static binary** — `go build` → one file; `make cross` → linux/amd64·arm64, darwin/arm64…
- ⚡ **Real-time** — IMAP IDLE (goroutine), seconds not polling
- 🧠 **Multi-provider with fallback** — `codex` (subscription) → `openai`/compatible → `ollama` (local)
- 🔎 **Agentic history lookup, on every capable provider** — when a mail looks like a thread/issue reply, the model can autonomously search related past mail before answering. `codex` uses its own agent loop; `openai` uses a **built-in function-calling loop** (no LangChain, ~one file). So you don't lose history context when falling back off `codex`.
- 🖼️ **Image emails OCR'd** — empty-body image mail → PaddleOCR before analysis
- 📱 **Smart multi-channel push** — urgent→break-through+sound, spam→silent, codes→copyable, tap→open in Gmail, grouped by category
- ♻️ **Reliable** — dedup watermark + retry queue + first-run baseline + IDLE auto-reconnect
- 🔒 **Safe** — read-only IMAP, body treated as untrusted, prompt-injection hardened; `codex` confined to a throwaway project-local sandbox

### 🚀 Quick start

```bash
go install github.com/Wangnov/mailpilot@latest
# or from source:
git clone https://github.com/Wangnov/mailpilot && cd mailpilot && make build
make cross           # static binaries for your server → dist/mailpilot-linux-arm64, etc.
```

```bash
mailpilot init                 # writes config.yaml
$EDITOR config.yaml            # IMAP + one provider + one notifier
export IMAP_PASSWORD=...  OPENAI_API_KEY=...  BARK_KEY=...
mailpilot run                  # process once (cron-friendly)
mailpilot daemon               # stay resident on IMAP IDLE (real-time)
```

> First run only records a watermark and does **not** push your existing backlog.

### ⚙️ Configuration

```yaml
imap:
  host: imap.gmail.com
  user: you@gmail.com
  password: ${IMAP_PASSWORD}     # Gmail: App Password
  force_ipv4: true               # set if your host's IPv6 egress is broken

analyze:
  providers:                     # tried in order; failure/rate-limit → next
    - type: codex                # your ChatGPT subscription (needs codex CLI)
      model: gpt-5.3-codex-spark
    - type: openai               # OpenAI or any compatible endpoint
      model: gpt-5.4-mini
      base_url: https://api.openai.com/v1
      api_key: ${OPENAI_API_KEY}
    # - type: ollama             # fully local, private, zero-cost
    #   model: qwen2.5
    #   base_url: http://localhost:11434
  timeout: 300

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
  #   url: ${WEBHOOK_URL}        # WeCom / Slack / custom

pipeline:
  baseline_on_first_run: true
  history_search: true
```

`${ENV}` is expanded on parsed string fields **after** YAML parsing, so secrets containing `: # "` etc. can't break parsing.

### 🧠 Providers & fallback

- **`codex`** — your ChatGPT subscription via the Codex CLI (`codex exec`). Saves API spend but can be rate-limited — **always put `openai`/`ollama` after it**. Agentic via codex's own loop, calling `mailpilot tool-search`. Runs **confined**: `--ephemeral` (no session files in `~/.codex`), temp artifacts only under `<config-dir>/.mailpilot-work/` and auto-cleaned, only ephemeral `-c`/`-m` overrides — it never edits your `~/.codex/config.toml` nor writes to system `/tmp` or your home dir (Linux & macOS alike).
- **`openai`** — OpenAI or any compatible endpoint (`base_url`). **Does agentic history search via a built-in function-calling loop**: the model calls `mail_search` over several rounds, then a final json-schema call produces strict structured output. The reliable workhorse.
- **`ollama`** — fully local, private, zero-cost. Single-shot (local models' tool-calling varies); use `openai`/`codex` for agentic history.

### 🔎 How agentic history works (no framework)

`tool-search` is a hidden subcommand of the **same binary** — a read-only IMAP search/get/thread. `codex` calls it inside its sandbox; `openai`'s loop shells out to it too. So the one binary is simultaneously the daemon *and* the tool the LLM calls. No agent framework, no extra service.

### 📱 Push

Channel capabilities are mapped from the analysis: urgent→break-through+sound, spam→silent, codes→copyable, tap→open in Gmail, grouped by category. Ships with **Bark / Telegram / ntfy / Webhook** (the Webhook payload is compatible with WeCom / Slack plain-text fields).

### 🚢 Deploy

```bash
scp dist/mailpilot-linux-arm64 server:/usr/local/bin/mailpilot   # one file, no deps
sudo cp deploy/mailpilot.service /etc/systemd/system/
sudo systemctl enable --now mailpilot
sudo journalctl -u mailpilot -f
```

### 🔒 Security

Read-only IMAP (App Password, never sends/deletes); email bodies treated as **untrusted** (URLs stripped, wrapped, prompt forbids executing any in-body instructions). The `codex` subprocess is **confined to the project**: a throwaway per-run sandbox (writes can't reach your `config.yaml`/`.env`), `--ephemeral` so nothing accumulates in `~/.codex`, your codex config left untouched, zero footprint in system temp or your home dir. All credentials revocable.

### 🛠 Build & release

```bash
make build        # current platform
make cross        # linux/amd64·arm64 + darwin/arm64·amd64 → dist/
make test         # go test -race ./...
make vet
```

Pushing a `v*` tag triggers the GitHub Actions **Release** workflow: cross-compile four static binaries, emit `SHA256SUMS.txt`, and publish a GitHub Release.

```bash
git tag v0.2.0 && git push origin v0.2.0
```

### 📄 License

MIT © 2026 wangnov
