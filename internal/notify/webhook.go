package notify

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/Wangnov/mailpilot/internal/config"
)

type webhookNotifier struct{ cfg config.Notifier }

func (n *webhookNotifier) Name() string { return "webhook" }

func (n *webhookNotifier) Send(m Message) error {
	if n.cfg.URL == "" {
		return fmt.Errorf("webhook 缺少 url")
	}
	format := webhookFormat(n.cfg)
	content := m.Title + "\n" + m.Body
	if format == "slack" {
		code, body, err := postJSON(n.cfg.URL, map[string]any{"text": content})
		if err != nil {
			return err
		}
		if code >= 300 || strings.TrimSpace(string(body)) != "ok" {
			return fmt.Errorf("webhook slack 返回 HTTP %d", code)
		}
		return nil
	}
	payload := map[string]any{
		"title": m.Title, "body": m.Body, "category": m.Category,
		"urgency": m.Urgency, "url": m.URL, "passive": m.Passive(),
		// 企业微信群机器人文本格式；generic 格式也保留这些结构化字段。
		"msgtype": "text",
		"text":    map[string]string{"content": content},
	}
	code, body, err := postJSON(n.cfg.URL, payload)
	if err != nil {
		return err
	}
	if code >= 300 {
		return fmt.Errorf("webhook 返回 HTTP %d", code)
	}
	if format == "wecom" && len(body) > 0 {
		var r struct {
			ErrCode int    `json:"errcode"`
			ErrMsg  string `json:"errmsg"`
		}
		if json.Unmarshal(body, &r) == nil && r.ErrCode != 0 {
			return fmt.Errorf("webhook wecom errcode=%d", r.ErrCode)
		}
	}
	return nil
}

func webhookFormat(cfg config.Notifier) string {
	format := strings.ToLower(strings.TrimSpace(cfg.Format))
	if format != "" {
		return format
	}
	u, err := url.Parse(cfg.URL)
	if err != nil {
		return "generic"
	}
	host := strings.ToLower(u.Hostname())
	switch {
	case strings.Contains(host, "qyapi.weixin.qq.com"):
		return "wecom"
	case strings.Contains(host, "hooks.slack.com"):
		return "slack"
	default:
		return "generic"
	}
}
