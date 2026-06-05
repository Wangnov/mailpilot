package notify

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/Wangnov/mailpilot/internal/config"
)

type ntfyNotifier struct{ cfg config.Notifier }

func (n *ntfyNotifier) Name() string { return "ntfy" }

func (n *ntfyNotifier) Send(m Message) error {
	if n.cfg.Topic == "" {
		return fmt.Errorf("ntfy 缺少 topic")
	}
	server := n.cfg.Server
	if server == "" {
		server = "https://ntfy.sh"
	}
	priority := "default"
	switch {
	case m.Passive():
		priority = "min"
	case m.High():
		priority = "urgent"
	}
	// 中文标题放正文首行，避开 ntfy header 仅 ASCII 的限制
	text := m.Title + "\n\n" + m.Body
	req, err := http.NewRequest("POST", strings.TrimRight(server, "/")+"/"+n.cfg.Topic, strings.NewReader(text))
	if err != nil {
		return err
	}
	req.Header.Set("Priority", priority)
	req.Header.Set("Tags", "email")
	if m.URL != "" {
		req.Header.Set("Click", m.URL)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("ntfy %d", resp.StatusCode)
	}
	return nil
}
