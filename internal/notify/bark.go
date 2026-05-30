package notify

import (
	"fmt"
	"strings"

	"github.com/Wangnov/mailpilot-go/internal/config"
)

type barkNotifier struct{ cfg config.Notifier }

func (n *barkNotifier) Name() string { return "bark" }

func (n *barkNotifier) Send(m Message) error {
	if n.cfg.Key == "" {
		return fmt.Errorf("bark 缺少 key")
	}
	server := n.cfg.Server
	if server == "" {
		server = "https://api.day.app"
	}
	payload := map[string]any{"title": m.Title, "body": m.Body, "group": m.Category}
	switch {
	case m.Passive():
		payload["level"] = "passive"
	case m.High():
		payload["level"] = "timeSensitive"
		payload["sound"] = "alarm"
	default:
		payload["level"] = "active"
	}
	if m.URL != "" {
		payload["url"] = m.URL
	}
	if m.Copy != "" {
		payload["copy"] = m.Copy
	}
	code, body, err := postJSON(strings.TrimRight(server, "/")+"/"+n.cfg.Key, payload)
	if err != nil {
		return err
	}
	if code != 200 || !strings.Contains(string(body), `"code":200`) {
		return fmt.Errorf("bark 返回: %s", string(body))
	}
	return nil
}
