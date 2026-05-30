package analyze

import (
	"strings"
	"testing"

	"github.com/Wangnov/mailpilot/internal/imap"
)

func TestSystemPromptLanguage(t *testing.T) {
	// 指定语言 → 注入该语言 + 输出语言子句
	p := systemPromptFor("English")
	if !strings.Contains(p, "English") || !strings.Contains(p, "【输出语言】") {
		t.Errorf("specified language not injected: %q", p)
	}
	// auto / 自动 / 空 → 随邮件本身语言
	for _, a := range []string{"auto", "自动", "", "  "} {
		if !strings.Contains(systemPromptFor(a), "邮件本身的主要语言") {
			t.Errorf("auto clause missing for %q", a)
		}
	}
	// 基础安全提示词应保留
	if !strings.Contains(systemPromptFor("中文"), "不可信") {
		t.Error("base prompt (安全规则) lost")
	}
}

func TestMailboxContext(t *testing.T) {
	// INBOX → 复用"已过反垃圾过滤"信号；星标/回复标记带出
	in := mailboxContext(&imap.Mail{Mailbox: "INBOX", Flags: []string{"\\Seen", "\\Flagged", "\\Answered"}})
	if !strings.Contains(in, "反垃圾") || !strings.Contains(in, "已加星标") || !strings.Contains(in, "已回复") {
		t.Errorf("INBOX signal/flags missing: %q", in)
	}
	// 非 INBOX 文件夹不应宣称"已过反垃圾"
	other := mailboxContext(&imap.Mail{Mailbox: "Archive"})
	if strings.Contains(other, "反垃圾") || !strings.Contains(other, "Archive") {
		t.Errorf("non-INBOX should not claim spam-filter: %q", other)
	}
	// 无 mailbox → 无上下文
	if mailboxContext(&imap.Mail{}) != "" {
		t.Error("empty mailbox should yield no context")
	}
}
