package notify

import (
	"fmt"
	"strings"

	"github.com/Wangnov/mailpilot/internal/config"
)

// defaultBarkIcon 是推送默认图标（mailpilot logo，经 jsDelivr CDN，国内可达且带缓存）。
// 可在 notify 配置里用 icon 字段覆盖：留空=用此默认，icon: "" 关闭，icon: <url> 自定义。
const defaultBarkIcon = "https://cdn.jsdelivr.net/gh/Wangnov/mailpilot@main/assets/icon.png"

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
	icon := defaultBarkIcon
	if n.cfg.Icon != nil { // 配置里显式给了 icon（含空串=关闭）
		icon = *n.cfg.Icon
	}
	if icon != "" {
		payload["icon"] = icon
	}
	code, body, err := postJSON(strings.TrimRight(server, "/")+"/"+n.cfg.Key, payload)
	if err != nil {
		return err
	}
	if code != 200 || !strings.Contains(string(body), `"code":200`) {
		return fmt.Errorf("bark 返回 HTTP %d", code)
	}
	return nil
}
