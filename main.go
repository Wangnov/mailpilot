// Command mailpilot — 极简推送式 AI 邮件管家（单二进制）。
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Wangnov/mailpilot/internal/config"
	"github.com/Wangnov/mailpilot/internal/imap"
	"github.com/Wangnov/mailpilot/internal/pipeline"
	goimap "github.com/emersion/go-imap/v2"
)

const configTemplate = `imap:
  host: imap.gmail.com
  user: you@gmail.com
  password: ${IMAP_PASSWORD}      # Gmail 用应用专用密码
  force_ipv4: true

analyze:
  providers:                      # 按序尝试，失败/限流自动降级到下一个
    - type: openai                # OpenAI 或任意兼容端点(可加 base_url)
      model: gpt-5.4-mini
      api_key: ${OPENAI_API_KEY}
    # - type: codex               # ChatGPT 订阅(本机需装 codex CLI)，单轮只读沙箱
    #   model: gpt-5.3-codex-spark
    # - type: ollama              # 本地模型，隐私优先零成本(单轮，无 agentic 历史)
    #   model: qwen2.5
    #   base_url: http://localhost:11434
    # - type: gemini              # Google Gemini(官方 Go SDK，单轮)
    #   model: gemini-3.1-flash-lite
    #   api_key: ${GEMINI_API_KEY}
  timeout: 300
  language: 中文                  # 通知语言：中文 / English / 日本語…；auto=随邮件本身语言

ocr:
  enabled: false                  # 需要图片邮件识别再开
  type: paddle                    # 当前内置 PaddleOCR；后续可扩展其它 OCR 引擎
  token: ${PADDLEOCR_TOKEN}

notify:
  - type: bark
    key: ${BARK_KEY}
    # icon: https://your.cdn/icon.png   # 推送图标；省略=内置 logo，设为 "" 关闭
  # - type: webhook
  #   url: ${WEBHOOK_URL}
  #   format: wecom              # 可选：generic / wecom / slack；常见企业微信/Slack URL 会自动识别

pipeline:
  baseline_on_first_run: true
  history_search: true
  # skip_categories: [垃圾, 营销推广]   # 命中的分类只分析、不推送；默认全部推送
  # scan_spam: true                    # 兜底扫垃圾箱，救回被邮箱误判的正常邮件(成本随垃圾量上升)
`

// version 由 release 构建经 -ldflags "-X main.version=<tag>" 注入。
var version = "dev"

func logln(s string) {
	fmt.Printf("[%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), s)
}

func usage() {
	fmt.Println(`mailpilot — 极简推送式 AI 邮件管家
用法:
  mailpilot init   [-c config.yaml]      生成配置模板
  mailpilot run    [-c config.yaml]      处理一次新邮件(适合 cron 兜底)
  mailpilot daemon [-c config.yaml]      常驻 IMAP IDLE，新邮件秒级触发
	  mailpilot tool-search <search|get|thread> ...   只读历史检索(供受限历史工具调用/人工排查)`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	cmd := os.Args[1]
	if cmd == "-h" || cmd == "--help" || cmd == "help" {
		usage()
		return
	}
	if cmd == "-v" || cmd == "--version" || cmd == "version" {
		fmt.Println("mailpilot", version)
		return
	}
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	cfgPath := fs.String("c", "config.yaml", "配置文件路径")
	maxN := fs.Int("max", 8, "tool-search 最大条数")
	_ = fs.Parse(os.Args[2:])

	switch cmd {
	case "init":
		cmdInit(*cfgPath)
	case "run":
		cmdRun(*cfgPath)
	case "daemon":
		cmdDaemon(*cfgPath)
	case "tool-search":
		cmdToolSearch(*cfgPath, *maxN, fs.Args())
	default:
		fmt.Println("未知命令:", cmd)
		usage()
		os.Exit(1)
	}
}

func loadCfg(path string) *config.Config {
	// 用绝对路径，确保 openai 派生的 tool-search 子进程无论 CWD 如何都能定位配置。
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	_ = os.Setenv("MAILPILOT_CONFIG", path)
	cfg, err := config.Load(path)
	if err != nil {
		fmt.Println("加载配置失败:", err)
		os.Exit(2)
	}
	return cfg
}

func cmdInit(path string) {
	if _, err := os.Stat(path); err == nil {
		fmt.Printf("已存在 %s，未覆盖。\n", path)
		return
	}
	if err := os.WriteFile(path, []byte(configTemplate), 0o600); err != nil {
		fmt.Println("写入失败:", err)
		return
	}
	fmt.Printf("已生成 %s。填好凭据(建议用环境变量)后运行 `mailpilot run` 或 `mailpilot daemon`。\n", path)
}

func cmdRun(path string) {
	p, err := pipeline.New(loadCfg(path), path, logln)
	if err != nil {
		fmt.Println(err)
		os.Exit(2)
	}
	if err := p.RunOnce(); err != nil {
		logln("运行失败: " + err.Error())
		os.Exit(1)
	}
}

func cmdDaemon(path string) {
	p, err := pipeline.New(loadCfg(path), path, logln)
	if err != nil {
		fmt.Println(err)
		os.Exit(2)
	}
	_ = p.Daemon()
}

func cmdToolSearch(path string, maxN int, args []string) {
	if env := os.Getenv("MAILPILOT_CONFIG"); env != "" {
		path = env // 供 openai agent loop 子进程复用同一配置
	}
	if len(args) < 1 {
		fmt.Println("用法: tool-search <search|get|thread> ...")
		return
	}
	box := imap.New(loadCfg(path).IMAP)
	if err := box.Connect(); err != nil {
		fmt.Println("连接失败:", err)
		return
	}
	defer box.Close()

	switch args[0] {
	case "search":
		if len(args) < 2 {
			fmt.Println("需要 query")
			return
		}
		uids, err := box.Search(buildCriteria(args[1]))
		if err != nil {
			fmt.Println("检索失败:", err)
			return
		}
		uids = reverseLimit(uids, maxN)
		if len(uids) == 0 {
			fmt.Println("（未找到相关历史邮件）")
			return
		}
		fmt.Printf("找到 %d 封相关历史（最新在前）：\n", len(uids))
		for _, uid := range uids {
			if m, _ := box.Fetch(uid, 0); m != nil {
				fmt.Printf("- uid=%d | %s | 发件人:%s | 主题:%s\n", uid, cut(m.Date, 25), cut(m.From, 40), cut(m.Subject, 60))
			}
		}
	case "get":
		if len(args) < 2 {
			return
		}
		uid, _ := strconv.ParseUint(args[1], 10, 32)
		if m, _ := box.Fetch(uint32(uid), 4000); m != nil {
			fmt.Printf("发件人: %s\n主题: %s\n日期: %s\n正文:\n%s\n", m.From, m.Subject, m.Date, m.Body)
		} else {
			fmt.Printf("（未找到 uid=%s）\n", args[1])
		}
	case "thread":
		if len(args) < 2 {
			return
		}
		uid, _ := strconv.ParseUint(args[1], 10, 32)
		m, _ := box.Fetch(uint32(uid), 0)
		if m == nil {
			fmt.Println("（未找到该邮件）")
			return
		}
		norm, ok := threadSearchSubject(m.Subject)
		if !ok {
			fmt.Printf("（未找到同主题历史）规范化主题=%q\n", norm)
			return
		}
		uids := reverseLimit(must(box.Search(buildCriteria("subject:"+norm))), maxN)
		if len(uids) == 0 {
			fmt.Printf("（未找到同主题历史）规范化主题=%q\n", norm)
			return
		}
		fmt.Printf("同主题《%s》共 %d 封：\n", norm, len(uids))
		for _, u := range uids {
			if mm, _ := box.Fetch(u, 1200); mm != nil {
				fmt.Println(formatThreadEntry(u, mm))
			}
		}
	default:
		fmt.Println("未知子命令:", args[0])
	}
}

func buildCriteria(query string) *goimap.SearchCriteria {
	c := &goimap.SearchCriteria{}
	switch {
	case strings.HasPrefix(query, "subject:"):
		c.Header = append(c.Header, goimap.SearchCriteriaHeaderField{Key: "Subject", Value: strings.TrimPrefix(query, "subject:")})
	case strings.HasPrefix(query, "from:"):
		c.Header = append(c.Header, goimap.SearchCriteriaHeaderField{Key: "From", Value: strings.TrimPrefix(query, "from:")})
	default:
		c.Text = []string{query}
	}
	return c
}

var reNorm = regexp.MustCompile(`(?i)^\s*((re|fwd|fw|答复|转发)\s*[:：]\s*)+`)

func normSubject(s string) string { return strings.TrimSpace(reNorm.ReplaceAllString(s, "")) }

func threadSearchSubject(subject string) (string, bool) {
	norm := normSubject(subject)
	return norm, len([]rune(norm)) >= 2
}

func reverseLimit(uids []uint32, n int) []uint32 {
	out := make([]uint32, 0, n)
	for i := len(uids) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, uids[i])
	}
	return out
}

func cut(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

func formatThreadEntry(uid uint32, m *imap.Mail) string {
	line := fmt.Sprintf("- uid=%d | %s | 发件人:%s | 主题:%s", uid, cut(m.Date, 25), cut(m.From, 40), cut(m.Subject, 80))
	if body := cut(oneLine(m.Body), 500); body != "" {
		line += "\n  正文摘录: " + body
	}
	return line
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func must(uids []uint32, _ error) []uint32 { return uids }
