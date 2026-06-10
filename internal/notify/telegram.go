package notify

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Wangnov/mailpilot/internal/config"
)

type telegramNotifier struct{ cfg config.Notifier }

func (n *telegramNotifier) Name() string { return "telegram" }

var tgEsc = regexp.MustCompile("([_*\\[\\]()`])")

func tgMarkdown(s string) string { return tgEsc.ReplaceAllString(s, `\$1`) }

func (n *telegramNotifier) Send(m Message) error {
	if n.cfg.BotToken == "" || n.cfg.ChatID == "" {
		return fmt.Errorf("telegram 缺少 bot_token/chat_id")
	}
	// 标题与正文都要转义：正文(摘要/要点)含 _ * [ ] ( ) ` 时
	// 否则 parse_mode=Markdown 会整条 400 失败。
	text := "*" + tgMarkdown(m.Title) + "*\n" + tgMarkdown(m.Body)
	payload := map[string]any{
		"chat_id": n.cfg.ChatID, "text": text, "parse_mode": "Markdown",
		"disable_notification": m.Passive(), "disable_web_page_preview": true,
	}
	if m.URL != "" {
		payload["reply_markup"] = map[string]any{
			"inline_keyboard": [][]map[string]string{{{"text": "在 Gmail 打开", "url": m.URL}}},
		}
	}
	server := n.cfg.Server
	if server == "" {
		server = "https://api.telegram.org"
	}
	api := strings.TrimRight(server, "/") + "/bot" + n.cfg.BotToken + "/sendMessage"
	code, body, err := postJSON(api, payload)
	if err != nil {
		return err
	}
	if code != 200 || !strings.Contains(string(body), `"ok":true`) {
		return fmt.Errorf("telegram 返回 HTTP %d", code)
	}
	return nil
}
