package notify

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wangnov/mailpilot/internal/config"
)

func TestBarkIcon(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = nil
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"code":200}`))
	}))
	defer srv.Close()

	send := func(icon *string) map[string]any {
		n := &barkNotifier{cfg: config.Notifier{Type: "bark", Key: "k", Server: srv.URL, Icon: icon}}
		if err := n.Send(Message{Title: "t", Body: "b", Category: "工作", Urgency: "中"}); err != nil {
			t.Fatalf("send: %v", err)
		}
		return got
	}

	// 不配置 icon → 用内置默认 logo
	if send(nil)["icon"] != defaultBarkIcon {
		t.Errorf("default icon = %v, want %s", send(nil)["icon"], defaultBarkIcon)
	}
	// 自定义 URL
	custom := "https://example.com/my.png"
	if send(&custom)["icon"] != custom {
		t.Errorf("custom icon not applied")
	}
	// 显式空串 → 不发 icon 字段
	empty := ""
	if _, ok := send(&empty)["icon"]; ok {
		t.Errorf(`icon should be omitted when set to ""`)
	}
}
