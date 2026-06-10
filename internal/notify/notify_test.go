package notify

import (
	"strings"
	"testing"

	"github.com/Wangnov/mailpilot/internal/analyze"
	"github.com/Wangnov/mailpilot/internal/imap"
)

func TestBuildMessageVerificationCode(t *testing.T) {
	m := BuildMessage(
		&imap.Mail{From: "Google", Subject: "验证码", MessageID: "<x@y>"},
		&analyze.Analysis{Category: "验证码", Urgency: "中", Summary: "登录验证码", KeyPoints: []string{"验证码：AB12-CD"}, VerificationCode: "AB12-CD"},
	)
	if m.Copy != "AB12-CD" {
		t.Errorf("copy=%q, want AB12-CD", m.Copy)
	}
	if want := "验证码: AB12-CD"; !contains(m.Body, want) {
		t.Errorf("body should include copyable code %q: %q", want, m.Body)
	}
	if m.URL == "" {
		t.Error("url should be set when MessageID present")
	}
	if m.Passive() {
		t.Error("验证码/中 should not be passive")
	}
}

func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}

func TestBuildMessageDoesNotInferCopyFromSummary(t *testing.T) {
	m := BuildMessage(
		&imap.Mail{From: "Google", Subject: "验证码", MessageID: "<x@y>"},
		&analyze.Analysis{Category: "验证码", Urgency: "中", Summary: "验证码 123456", KeyPoints: []string{"请输入 123456"}, VerificationCode: ""},
	)
	if m.Copy != "" {
		t.Errorf("copy=%q, want empty when verification_code is empty", m.Copy)
	}
}

func TestBuildMessagePrefersActionURL(t *testing.T) {
	m := BuildMessage(
		&imap.Mail{Subject: "确认登录", MessageID: "<x@y>"},
		&analyze.Analysis{Category: "验证码", Urgency: "中", Summary: "确认登录", ActionURL: "https://example.com/verify?token=abc"},
	)
	if m.URL != "https://example.com/verify?token=abc" {
		t.Errorf("url=%q, want action_url", m.URL)
	}
}

func TestBuildMessageRejectsUnsafeActionURL(t *testing.T) {
	m := BuildMessage(
		&imap.Mail{Subject: "确认登录", MessageID: "<x@y>"},
		&analyze.Analysis{Category: "验证码", Urgency: "中", Summary: "确认登录", ActionURL: "javascript:alert(1)"},
	)
	if m.URL == "" || m.URL == "javascript:alert(1)" {
		t.Errorf("unsafe action_url should fall back to Gmail URL, got %q", m.URL)
	}
}

func TestBuildMessagePassiveAndFormat(t *testing.T) {
	mk := BuildMessage(&imap.Mail{Subject: "促销"}, &analyze.Analysis{Category: "营销推广", Urgency: "低"})
	if !mk.Passive() {
		t.Error("营销推广/低 should be passive")
	}
	hi := BuildMessage(&imap.Mail{From: "boss", Subject: "紧急"},
		&analyze.Analysis{Category: "工作", Urgency: "高", Summary: "s", KeyPoints: []string{"a"}})
	if hi.Passive() || !hi.High() {
		t.Error("工作/高 should be high, not passive")
	}
	if len(hi.Body) == 0 || hi.Body[:len("👤")] != "👤" {
		t.Errorf("body should start with 👤, got %q", hi.Body)
	}
}
