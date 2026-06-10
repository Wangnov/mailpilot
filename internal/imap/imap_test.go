package imap

import (
	"strings"
	"testing"
)

func TestClip(t *testing.T) {
	// name 既是用例说明也避免 CJK 行尾注释引发的 gofmt 跨版本对齐差异
	cases := []struct {
		name string
		s    string
		max  int
		want string
	}{
		{"max<=0 不截断", "hello", 0, "hello"},
		{"未超长", "hello", 10, "hello"},
		{"ASCII 截断", "hello", 3, "hel"},
		{"7 字节落在“世”中间，回退到完整字符", "你好世界", 7, "你好"},
		{"正好落在字符边界", "你好世界", 6, "你好"},
		{"中文未超长(12 字节)", "你好世界", 100, "你好世界"},
		{"合法 U+FFFD(3字节)不应被误删", "�x", 3, "�"},
	}
	for _, c := range cases {
		if got := clip(c.s, c.max); got != c.want {
			t.Errorf("%s: clip(%q,%d)=%q, want %q", c.name, c.s, c.max, got, c.want)
		}
	}
}

func TestParseMailPrefersPlainAndCollectsImages(t *testing.T) {
	raw := crlf(`From: Alice <alice@example.com>
Subject: Report
Date: Sat, 30 May 2026 10:00:00 +0800
Message-ID: <msg-1@example.com>
MIME-Version: 1.0
Content-Type: multipart/mixed; boundary="b"

--b
Content-Type: text/plain; charset=utf-8

plain body
--b
Content-Type: text/html; charset=utf-8

<p>html body</p>
--b
Content-Type: image/png
Content-Disposition: attachment; filename="shot.png"

` + strings.Repeat("x", 2050) + `
--b--`)

	m := parseMail(42, []byte(raw), 0)
	if m.UID != 42 || m.Subject != "Report" || !strings.Contains(m.From, "alice@example.com") {
		t.Fatalf("headers parsed wrong: %+v", m)
	}
	if m.MessageID != "<msg-1@example.com>" {
		t.Fatalf("message id=%q", m.MessageID)
	}
	if strings.TrimSpace(m.Body) != "plain body" {
		t.Fatalf("body=%q", m.Body)
	}
	if len(m.Images) != 1 || len(m.Images[0]) < 2048 {
		t.Fatalf("images=%d len=%d", len(m.Images), imageLen(m.Images))
	}
}

func TestParseMailFallsBackToStrippedHTML(t *testing.T) {
	raw := crlf(`From: Bob <bob@example.com>
Subject: HTML only
MIME-Version: 1.0
Content-Type: text/html; charset=utf-8

<html><body><style>.x{}</style><p>Hello <b>世界</b></p><a href="https://example.com/action?token=abc">确认</a><script>alert(1)</script></body></html>`)

	m := parseMail(7, []byte(raw), 0)
	if m.Body != "Hello 世界 确认 (https://example.com/action?token=abc)" {
		t.Fatalf("html body=%q", m.Body)
	}
}

func TestParseMailKeepsImageOnlyHTMLLink(t *testing.T) {
	raw := crlf(`From: Bob <bob@example.com>
Subject: Image button
MIME-Version: 1.0
Content-Type: text/html; charset=utf-8

<html><body><a href="https://example.com/confirm?token=abc"><img src="cid:button"></a></body></html>`)

	m := parseMail(9, []byte(raw), 0)
	if m.Body != "[HTML 链接]\nhttps://example.com/confirm?token=abc" {
		t.Fatalf("image-only html link should be preserved: %q", m.Body)
	}
}

func TestParseMailKeepsHTMLLinksAlongsidePlain(t *testing.T) {
	raw := crlf(`From: Bob <bob@example.com>
Subject: Mixed
MIME-Version: 1.0
Content-Type: multipart/alternative; boundary="b"

--b
Content-Type: text/plain; charset=utf-8

plain body
--b
Content-Type: text/html; charset=utf-8

<a href="https://example.com/pay?id=1">Pay now</a><a href="javascript:alert(1)">bad</a>
--b--`)

	m := parseMail(8, []byte(raw), 0)
	if !strings.Contains(m.Body, "plain body\n\n[HTML 链接]\nhttps://example.com/pay?id=1") {
		t.Fatalf("mixed body should keep html link: %q", m.Body)
	}
	if strings.Contains(m.Body, "javascript:") {
		t.Fatalf("unsafe link should be dropped: %q", m.Body)
	}
}

func TestParseMailKeepsLateHTMLLinkWhenBodyIsClipped(t *testing.T) {
	link := "https://example.com/pay?id=late"
	raw := crlf(`From: Bob <bob@example.com>
Subject: Long mixed
MIME-Version: 1.0
Content-Type: multipart/alternative; boundary="b"

--b
Content-Type: text/plain; charset=utf-8

` + strings.Repeat("x", 80) + ` ` + link + `
--b
Content-Type: text/html; charset=utf-8

<a href="` + link + `">Pay now</a>
--b--`)

	m := parseMail(10, []byte(raw), 70)
	if len(m.Body) > 70 {
		t.Fatalf("body should be clipped to budget: len=%d body=%q", len(m.Body), m.Body)
	}
	if !strings.Contains(m.Body, "[HTML 链接]\n"+link) {
		t.Fatalf("late link should be preserved after clipping: %q", m.Body)
	}
}

func TestParseMailDropsBodyWhenHTMLLinkBlockConsumesBudget(t *testing.T) {
	link := "https://example.com/pay"
	raw := crlf(`From: Bob <bob@example.com>
Subject: Budget
MIME-Version: 1.0
Content-Type: multipart/alternative; boundary="b"

--b
Content-Type: text/plain; charset=utf-8

` + strings.Repeat("x", 200) + `
--b
Content-Type: text/html; charset=utf-8

<a href="` + link + `">Pay now</a>
--b--`)

	m := parseMail(11, []byte(raw), len("[HTML 链接]\n"+link))
	if m.Body != "[HTML 链接]\n"+link {
		t.Fatalf("body should only contain bounded link block: len=%d body=%q", len(m.Body), m.Body)
	}
}

func TestParseMailRawFallbackIsClipped(t *testing.T) {
	m := parseMail(1, []byte("not a mime message 你好世界"), 24)
	if m.Body != "not a mime message 你" {
		t.Fatalf("fallback body=%q", m.Body)
	}
}

func crlf(s string) string {
	return strings.ReplaceAll(s, "\n", "\r\n")
}

func imageLen(images [][]byte) int {
	if len(images) == 0 {
		return 0
	}
	return len(images[0])
}
