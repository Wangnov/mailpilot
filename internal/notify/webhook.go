package notify

import (
	"fmt"

	"github.com/Wangnov/mailpilot/internal/config"
)

type webhookNotifier struct{ cfg config.Notifier }

func (n *webhookNotifier) Name() string { return "webhook" }

func (n *webhookNotifier) Send(m Message) error {
	if n.cfg.URL == "" {
		return fmt.Errorf("webhook 缺少 url")
	}
	payload := map[string]any{
		"title": m.Title, "body": m.Body, "category": m.Category,
		"urgency": m.Urgency, "url": m.URL, "passive": m.Passive(),
		// 兼容企业微信群机器人 / Slack 的纯文本字段
		"msgtype": "text",
		"text":    map[string]string{"content": m.Title + "\n" + m.Body},
	}
	code, body, err := postJSON(n.cfg.URL, payload)
	if err != nil {
		return err
	}
	if code >= 300 {
		return fmt.Errorf("webhook %d: %s", code, string(body))
	}
	return nil
}
