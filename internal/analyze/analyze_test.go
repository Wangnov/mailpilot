package analyze

import (
	"strings"
	"testing"
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
