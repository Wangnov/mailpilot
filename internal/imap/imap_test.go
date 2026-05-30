package imap

import "testing"

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
