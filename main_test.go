package main

import (
	"strings"
	"testing"

	"github.com/Wangnov/mailpilot/internal/imap"
)

func TestFormatThreadEntryIncludesBodyExcerpt(t *testing.T) {
	m := &imap.Mail{
		Subject: "Re: 合同确认",
		From:    "Alice <alice@example.com>",
		Date:    "Thu, 11 Jun 2026 12:00:00 +0800",
		Body:    "上一封里确认了报价。\n请按附件里的付款信息处理。",
	}
	got := formatThreadEntry(42, m)
	for _, want := range []string{"uid=42", "主题:Re: 合同确认", "正文摘录: 上一封里确认了报价。 请按附件里的付款信息处理。"} {
		if !strings.Contains(got, want) {
			t.Fatalf("thread entry missing %q:\n%s", want, got)
		}
	}
}

func TestThreadSearchSubjectRejectsEmptyOrTooShortSubjects(t *testing.T) {
	cases := []struct {
		name string
		in   string
		norm string
		ok   bool
	}{
		{"empty after reply prefix", "Re:", "", false},
		{"one rune is too broad", "Fwd: A", "A", false},
		{"normal zh subject", "答复: 合同", "合同", true},
	}
	for _, c := range cases {
		gotNorm, gotOK := threadSearchSubject(c.in)
		if gotNorm != c.norm || gotOK != c.ok {
			t.Fatalf("%s: threadSearchSubject(%q)=(%q,%v), want (%q,%v)", c.name, c.in, gotNorm, gotOK, c.norm, c.ok)
		}
	}
}
