// Package notify 把分析结果按 B 版格式推送到多渠道（智能 level/group/copy/url）。
package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
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

var codeRe = regexp.MustCompile(`\b\d{4,8}\b`)

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
	if id := strings.Trim(mail.MessageID, "<>"); id != "" {
		u = "https://mail.google.com/mail/u/0/#search/" + url.QueryEscape("rfc822msgid:"+id)
	}
	if a.Category == "验证码" {
		cp = codeRe.FindString(a.Summary + " " + strings.Join(a.KeyPoints, " "))
	}
	return Message{
		Title: truncate(title, 120), Body: strings.Join(lines, "\n"),
		Category: a.Category, Urgency: a.Urgency, URL: u, Copy: cp,
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
