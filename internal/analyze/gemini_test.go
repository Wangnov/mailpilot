package analyze

import (
	"os"
	"testing"

	"github.com/Wangnov/mailpilot/internal/config"
	"github.com/Wangnov/mailpilot/internal/imap"
)

func TestGeminiSchema(t *testing.T) {
	s := geminiSchema()
	if len(s.Required) != 8 {
		t.Errorf("required=%d, want 8", len(s.Required))
	}
	if c := s.Properties["category"]; c == nil || len(c.Enum) != 9 {
		t.Error("category 枚举应有 9 个值")
	}
	if u := s.Properties["urgency"]; u == nil || len(u.Enum) != 3 {
		t.Error("urgency 枚举应有 3 个值")
	}
	if kp := s.Properties["key_points"]; kp == nil || kp.Items == nil {
		t.Error("key_points 应为带 items 的数组")
	}
	if au := s.Properties["action_url"]; au == nil {
		t.Error("action_url 应存在")
	}
	if vc := s.Properties["verification_code"]; vc == nil {
		t.Error("verification_code 应存在")
	}
}

// 用本机/真实 key 端到端验证 Gemini provider。默认跳过；
// 设 GEMINI_E2E=1 + GEMINI_API_KEY(+可选 GEMINI_MODEL) 运行。
func TestGeminiProviderE2E(t *testing.T) {
	if os.Getenv("GEMINI_E2E") != "1" {
		t.Skip("设 GEMINI_E2E=1 运行 Gemini 端到端测试")
	}
	key := os.Getenv("GEMINI_API_KEY")
	if key == "" {
		t.Skip("需要 GEMINI_API_KEY")
	}
	model := os.Getenv("GEMINI_MODEL")
	if model == "" {
		model = "gemini-3.1-flash-lite"
	}
	p, err := newGeminiProvider(config.Provider{Type: "gemini", Model: model, APIKey: key}, 120, "中文")
	if err != nil {
		t.Fatalf("newGeminiProvider: %v", err)
	}
	m := &imap.Mail{
		UID: 1, From: "Google <no-reply@accounts.google.com>",
		Subject: "安全提醒：检测到新设备登录", Date: "Sat, 31 May 2026 10:00:00 +0800",
		Mailbox: "INBOX",
		Body:    "我们检测到您的 Google 账号在一台新设备登录。验证码：294817。",
	}
	a, err := p.Analyze(m, false, "")
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if a.Category == "" || a.Urgency == "" {
		t.Fatalf("关键字段为空: %+v", a)
	}
	t.Logf("gemini[%s]: category=%s urgency=%s needs_reply=%v summary=%q points=%v",
		model, a.Category, a.Urgency, a.NeedsReply, a.Summary, a.KeyPoints)
}
