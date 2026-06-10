// Package notify 把分析结果按 B 版格式推送到多渠道（智能 level/group/copy/url）。
package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Wangnov/mailpilot/internal/analyze"
	"github.com/Wangnov/mailpilot/internal/config"
	"github.com/Wangnov/mailpilot/internal/imap"
)

type Message struct {
	Title, Body, Category, Urgency, URL, Copy string
}

func (m Message) Passive() bool {
	return m.Category == "垃圾" || m.Category == "营销推广" || m.Urgency == "低"
}
func (m Message) High() bool { return m.Urgency == "高" }

func BuildMessage(mail *imap.Mail, a *analyze.Analysis) Message {
	lines := []string{"👤 " + truncate(mail.From, 80), "💬 " + a.Summary}
	for i, p := range a.KeyPoints {
		if i >= 6 {
			break
		}
		lines = append(lines, "• "+p)
	}
	title := mail.Subject
	if title == "" {
		title = "(无主题)"
	}
	var u, cp string
	if actionURL := validatedActionURL(a.ActionURL); actionURL != "" {
		u = actionURL
	} else if id := strings.Trim(mail.MessageID, "<>"); id != "" {
		u = "https://mail.google.com/mail/u/0/#search/" + url.QueryEscape("rfc822msgid:"+id)
	}
	cp = copyableVerificationCode(a.VerificationCode)
	return Message{
		Title: truncate(title, 120), Body: strings.Join(lines, "\n"),
		Category: a.Category, Urgency: a.Urgency, URL: u, Copy: cp,
	}
}

func copyableVerificationCode(raw string) string {
	code := strings.Trim(strings.TrimSpace(raw), "\"'")
	code = strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(code)
	if len([]rune(code)) > 128 {
		return truncate(code, 128)
	}
	return code
}

func validatedActionURL(raw string) string {
	raw = strings.Trim(strings.TrimSpace(raw), "<>\"'")
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Host == "" {
		return ""
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return u.String()
	default:
		return ""
	}
}

type Notifier interface {
	Name() string
	Send(m Message) error
}

func BuildNotifier(cfg config.Notifier) (Notifier, error) {
	switch cfg.Type {
	case "bark":
		return &barkNotifier{cfg}, nil
	case "telegram":
		return &telegramNotifier{cfg}, nil
	case "ntfy":
		return &ntfyNotifier{cfg}, nil
	case "webhook":
		return &webhookNotifier{cfg}, nil
	}
	return nil, fmt.Errorf("未知 notifier 类型: %s", cfg.Type)
}

// NotifyAll 推送所有渠道；全部成功才 true（任一失败则不标记已处理、留待重试）。
func NotifyAll(notifiers []Notifier, mail *imap.Mail, a *analyze.Analysis, log func(string)) bool {
	m := BuildMessage(mail, a)
	ok := true
	for _, n := range notifiers {
		if err := n.Send(m); err != nil {
			ok = false
			log(fmt.Sprintf("通知渠道 %s 失败: %s", n.Name(), err.Error()))
		}
	}
	return ok
}

var httpClient = &http.Client{Timeout: 20 * time.Second}

func postJSON(u string, payload any) (int, []byte, error) {
	buf, _ := json.Marshal(payload)
	resp, err := httpClient.Post(u, "application/json; charset=utf-8", bytes.NewReader(buf))
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b, nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
