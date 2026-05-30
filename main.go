// Command mailpilot — 极简推送式 AI 邮件管家（单二进制）。
package main

import (
	"flag"
	"fmt"
	"os"
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
  providers:                      # 按序尝试，失败/限流自动降级
    - type: openai                # 或 codex(ChatGPT 订阅) / ollama(本地)
      model: gpt-4o-mini
      api_key: ${OPENAI_API_KEY}
  timeout: 300

ocr:
  enabled: false                  # 需要图片邮件识别再开
  token: ${PADDLEOCR_TOKEN}

notify:
  - type: bark
    key: ${BARK_KEY}

pipeline:
  baseline_on_first_run: true
  history_search: true
`

func logln(s string) {
	fmt.Printf("[%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), s)
}

func usage() {
	fmt.Println(`mailpilot — 极简推送式 AI 邮件管家
用法:
  mailpilot init   [-c config.yaml]      生成配置模板
  mailpilot run    [-c config.yaml]      处理一次新邮件(适合 cron 兜底)
  mailpilot daemon [-c config.yaml]      常驻 IMAP IDLE，新邮件秒级触发
  mailpilot tool-search <search|get|thread> ...   只读历史检索(供 agentic 调用)`)
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
		norm := normSubject(m.Subject)
		uids := reverseLimit(must(box.Search(buildCriteria("subject:"+norm))), maxN)
		if len(uids) == 0 {
			fmt.Printf("（未找到同主题历史）规范化主题=%q\n", norm)
			return
		}
		fmt.Printf("同主题《%s》共 %d 封：\n", norm, len(uids))
		for _, u := range uids {
			if mm, _ := box.Fetch(u, 0); mm != nil {
				fmt.Printf("- uid=%d | %s | 发件人:%s\n", u, cut(mm.Date, 25), cut(mm.From, 40))
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

func must(uids []uint32, _ error) []uint32 { return uids }
