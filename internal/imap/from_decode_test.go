package imap

import "testing"

// 回归：发件人显示名的 RFC 2047 encoded-word 必须被解码成明文，
// 不能在通知里留下 =?utf-8?q?…?= 这种乱码，也不能被重新编码成 =?utf-8?b?…?=。
func TestParseMailFromDecode(t *testing.T) {
	cases := []struct {
		name string
		from string // From 头原文
		want string
	}{
		{
			name: "utf8_q_single_word", // 截图里那封：单个 encoded-word 紧跟 <地址>
			from: "=?utf-8?q?=E6=B9=96=E5=87=B6=E9=99=A4=E4=BD=A0=E8=BF=98=E6=98=AF?= <noreply@linux.do>",
			want: "湖凶除你还是 <noreply@linux.do>",
		},
		{
			name: "utf8_q_multi_word", // Discourse 把长显示名拆成多个 encoded-word
			from: "=?UTF-8?Q?=E6=B9=96=E5=87=B6=E9=99=A4=E4=BD=A0=E8=BF=98=E6=98=AF?= =?UTF-8?Q?_via_linux=2Edo?= <noreply@linux.do>",
			want: "湖凶除你还是 via linux.do <noreply@linux.do>",
		},
		{
			name: "gbk_b_word", // 非 UTF-8 字符集（国产邮箱常见）
			from: "=?gbk?B?us/Su87E?= <user@example.com>",
			want: "合一文 <user@example.com>",
		},
		{
			name: "plain_ascii", // 普通英文名不应被破坏
			from: "John Doe <john@example.com>",
			want: "John Doe <john@example.com>",
		},
		{
			name: "bare_address", // 无显示名
			from: "noreply@example.com",
			want: "noreply@example.com",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw := "From: " + c.from + "\r\nSubject: t\r\n\r\nbody\r\n"
			m := parseMail(1, []byte(raw), 0)
			if m.From != c.want {
				t.Errorf("From=%q, want %q", m.From, c.want)
			}
		})
	}
}

// 顺带验证：导入 go-message/charset 后，非 UTF-8 的【主题】也能解码（此前会失败/乱码）。
func TestParseMailSubjectGBK(t *testing.T) {
	raw := "From: a@b.com\r\nSubject: =?gbk?B?us/Su87E?=\r\n\r\nbody\r\n"
	m := parseMail(1, []byte(raw), 0)
	if m.Subject != "合一文" {
		t.Errorf("Subject=%q, want %q", m.Subject, "合一文")
	}
}
