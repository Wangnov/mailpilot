package pipeline

import (
	"testing"

	"github.com/Wangnov/mailpilot/internal/imap"
)

func TestLikelyHasHistory(t *testing.T) {
	cases := []struct {
		m    *imap.Mail
		want bool
	}{
		{&imap.Mail{Subject: "Re: 项目周会"}, true},
		{&imap.Mail{Subject: "答复：合同"}, true},
		{&imap.Mail{Subject: "bug report", Body: "see issue #42"}, true},
		{&imap.Mail{Subject: "hello", Body: "just saying hi"}, false},
	}
	for i, c := range cases {
		if got := likelyHasHistory(c.m); got != c.want {
			t.Errorf("case %d: got %v want %v (subject=%q)", i, got, c.want, c.m.Subject)
		}
	}
}
