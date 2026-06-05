package notify

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wangnov/mailpilot/internal/config"
)

func TestTelegramNotifierSend(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/botbot-token/sendMessage" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	n := &telegramNotifier{cfg: config.Notifier{BotToken: "bot-token", ChatID: "chat", Server: srv.URL}}
	err := n.Send(Message{Title: "A_[x]", Body: "B_(y)", Category: "工作", Urgency: "低", URL: "https://mail.google.com/x"})
	if err != nil {
		t.Fatal(err)
	}
	if got["chat_id"] != "chat" || got["parse_mode"] != "Markdown" {
		t.Fatalf("telegram payload=%v", got)
	}
	text, _ := got["text"].(string)
	if !strings.Contains(text, `A\_\[x\]`) || !strings.Contains(text, `B\_\(y\)`) {
		t.Fatalf("markdown was not escaped: %q", text)
	}
	if got["disable_notification"] != true {
		t.Fatalf("passive message should disable notification: %v", got)
	}
}

func TestTelegramNotifierFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"ok":false}`))
	}))
	defer srv.Close()

	n := &telegramNotifier{cfg: config.Notifier{BotToken: "t", ChatID: "c", Server: srv.URL}}
	if err := n.Send(Message{Title: "t", Body: "b"}); err == nil {
		t.Fatal("expected telegram failure")
	}
}

func TestNtfyNotifierSend(t *testing.T) {
	var gotBody string
	var gotPriority, gotClick string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/topic-a" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		gotPriority = r.Header.Get("Priority")
		gotClick = r.Header.Get("Click")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := &ntfyNotifier{cfg: config.Notifier{Topic: "topic-a", Server: srv.URL}}
	err := n.Send(Message{Title: "紧急", Body: "处理一下", Category: "工作", Urgency: "高", URL: "https://mail.google.com/x"})
	if err != nil {
		t.Fatal(err)
	}
	if gotPriority != "urgent" || gotClick == "" || !strings.Contains(gotBody, "紧急\n\n处理一下") {
		t.Fatalf("ntfy priority=%q click=%q body=%q", gotPriority, gotClick, gotBody)
	}
}

func TestWebhookNotifierSend(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	n := &webhookNotifier{cfg: config.Notifier{URL: srv.URL}}
	err := n.Send(Message{Title: "标题", Body: "正文", Category: "营销推广", Urgency: "低", URL: "https://mail.google.com/x"})
	if err != nil {
		t.Fatal(err)
	}
	if got["msgtype"] != "text" || got["passive"] != true {
		t.Fatalf("webhook payload=%v", got)
	}
	text, _ := got["text"].(map[string]any)
	if text["content"] != "标题\n正文" {
		t.Fatalf("webhook text=%v", text)
	}
}

func TestBuildNotifierErrors(t *testing.T) {
	if _, err := BuildNotifier(config.Notifier{Type: "missing"}); err == nil {
		t.Fatal("unknown notifier should fail")
	}
	if err := (&telegramNotifier{}).Send(Message{}); err == nil {
		t.Fatal("telegram without credentials should fail")
	}
	if err := (&ntfyNotifier{}).Send(Message{}); err == nil {
		t.Fatal("ntfy without topic should fail")
	}
	if err := (&webhookNotifier{}).Send(Message{}); err == nil {
		t.Fatal("webhook without url should fail")
	}
}
