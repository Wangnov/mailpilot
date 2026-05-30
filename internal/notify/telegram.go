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

func (n *telegramNotifier) Send(m Message) error {
	if n.cfg.BotToken == "" || n.cfg.ChatID == "" {
		return fmt.Errorf("telegram 缺少 bot_token/chat_id")
	}
	text := "*" + tgEsc.ReplaceAllString(m.Title, `\$1`) + "*\n" + m.Body
	if m.URL != "" {
		text += "\n[在 Gmail 打开](" + m.URL + ")"
	}
	payload := map[string]any{
		"chat_id": n.cfg.ChatID, "text": text, "parse_mode": "Markdown",
		"disable_notification": m.Passive(), "disable_web_page_preview": true,
	}
	api := "https://api.telegram.org/bot" + n.cfg.BotToken + "/sendMessage"
	code, body, err := postJSON(api, payload)
	if err != nil {
		return err
	}
	if code != 200 || !strings.Contains(string(body), `"ok":true`) {
		return fmt.Errorf("telegram: %s", string(body))
	}
	return nil
}
