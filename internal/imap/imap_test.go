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

<html><body><style>.x{}</style><p>Hello <b>世界</b></p><script>alert(1)</script></body></html>`)

	m := parseMail(7, []byte(raw), 0)
	if m.Body != "Hello 世界" {
		t.Fatalf("html body=%q", m.Body)
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
