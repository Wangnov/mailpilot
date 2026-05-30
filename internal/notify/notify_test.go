package notify

import (
	"testing"

	"github.com/Wangnov/mailpilot/internal/analyze"
	"github.com/Wangnov/mailpilot/internal/imap"
)

func TestBuildMessageVerificationCode(t *testing.T) {
	m := BuildMessage(
		&imap.Mail{From: "Google", Subject: "验证码", MessageID: "<x@y>"},
		&analyze.Analysis{Category: "验证码", Urgency: "中", Summary: "登录验证码 123456", KeyPoints: []string{"验证码：123456"}},
	)
	if m.Copy != "123456" {
		t.Errorf("copy=%q, want 123456", m.Copy)
	}
	if m.URL == "" {
		t.Error("url should be set when MessageID present")
	}
	if m.Passive() {
		t.Error("验证码/中 should not be passive")
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
