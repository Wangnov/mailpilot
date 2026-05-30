// Package imap 负责 IMAP 取信、MIME 解析与 IDLE 守护（go-imap v2 + go-message）。
package imap

import (
	"bytes"
	"crypto/tls"
	"io"
	"net"
	"regexp"
	"strings"
	"time"

	"github.com/Wangnov/mailpilot-go/internal/config"
	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message/mail"
)

type Mail struct {
	UID       uint32
	From      string
	Subject   string
	Date      string
	MessageID string
	Body      string
	Images    [][]byte
}

type Box struct {
	cfg   config.IMAP
	c     *imapclient.Client
	newCh chan struct{}
}

func New(cfg config.IMAP) *Box {
	return &Box{cfg: cfg, newCh: make(chan struct{}, 1)}
}

// Connect 拨号(可强制 IPv4)+TLS+登录+只读选择邮箱。
func (b *Box) Connect() error {
	network := "tcp"
	if b.cfg.ForceIPv4 {
		network = "tcp4" // 本机 IPv6 出站不通时强制 IPv4，避免连接卡超时
	}
	conn, err := net.DialTimeout(network, b.cfg.Host+":993", 30*time.Second)
	if err != nil {
		return err
	}
	tlsConn := tls.Client(conn, &tls.Config{ServerName: b.cfg.Host})
	if err := tlsConn.Handshake(); err != nil {
		return err
	}
	opts := &imapclient.Options{
		UnilateralDataHandler: &imapclient.UnilateralDataHandler{
			Mailbox: func(d *imapclient.UnilateralDataMailbox) {
				if d.NumMessages != nil { // 新邮件 → 非阻塞通知
					select {
					case b.newCh <- struct{}{}:
					default:
					}
				}
			},
		},
	}
	c := imapclient.New(tlsConn, opts)
	if err := c.Login(b.cfg.User, b.cfg.Password).Wait(); err != nil {
		c.Close()
		return err
	}
	if _, err := c.Select(b.cfg.Mailbox, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		c.Close()
		return err
	}
	b.c = c
	return nil
}

func (b *Box) Close() {
	if b.c != nil {
		b.c.Logout().Wait()
		b.c.Close()
		b.c = nil
	}
}

func (b *Box) UIDValidity() (uint32, error) {
	data, err := b.c.Status(b.cfg.Mailbox, &imap.StatusOptions{UIDValidity: true}).Wait()
	if err != nil {
		return 0, err
	}
	return data.UIDValidity, nil
}

func toU32(uids []imap.UID) []uint32 {
	out := make([]uint32, len(uids))
	for i, u := range uids {
		out[i] = uint32(u)
	}
	return out
}

// AllUIDs 返回邮箱内全部 UID（空 criteria = ALL）。
func (b *Box) AllUIDs() ([]uint32, error) {
	data, err := b.c.UIDSearch(&imap.SearchCriteria{}, nil).Wait()
	if err != nil {
		return nil, err
	}
	return toU32(data.AllUIDs()), nil
}

// Search 通用 IMAP 检索（供 agentic 历史检索工具用）。
func (b *Box) Search(criteria *imap.SearchCriteria) ([]uint32, error) {
	data, err := b.c.UIDSearch(criteria, nil).Wait()
	if err != nil {
		return nil, err
	}
	return toU32(data.AllUIDs()), nil
}

// Fetch 取一封邮件的完整内容并解析。
func (b *Box) Fetch(uid uint32, maxBody int) (*Mail, error) {
	bs := &imap.FetchItemBodySection{}
	opts := &imap.FetchOptions{UID: true, BodySection: []*imap.FetchItemBodySection{bs}}
	cmd := b.c.Fetch(imap.UIDSetNum(imap.UID(uid)), opts)
	defer cmd.Close()
	msg := cmd.Next()
	if msg == nil {
		return nil, nil
	}
	buf, err := msg.Collect()
	if err != nil {
		return nil, err
	}
	raw := buf.FindBodySection(bs)
	return parseMail(uid, raw, maxBody), nil
}

// IdleLoop 进入 IDLE，新邮件触发 onNew；每 timeout 重置一次 IDLE。断连返回错误由外层重连。
func (b *Box) IdleLoop(onNew func(), timeout time.Duration) error {
	for {
		idleCmd, err := b.c.Idle()
		if err != nil {
			return err
		}
		timer := time.NewTimer(timeout)
		select {
		case <-b.newCh:
			timer.Stop()
			idleCmd.Close()
			if err := idleCmd.Wait(); err != nil {
				return err
			}
			onNew()
		case <-timer.C:
			idleCmd.Close()
			if err := idleCmd.Wait(); err != nil {
				return err
			}
		}
	}
}

// ---- MIME 解析 ----

var (
	scriptRe = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
	tagRe    = regexp.MustCompile(`(?s)<[^>]+>`)
	urlRe    = regexp.MustCompile(`https?://\S+`)
	wsRe     = regexp.MustCompile(`\s+`)
)

func stripHTML(h string) string {
	h = scriptRe.ReplaceAllString(h, " ")
	h = tagRe.ReplaceAllString(h, " ")
	return strings.TrimSpace(wsRe.ReplaceAllString(h, " "))
}

func parseMail(uid uint32, raw []byte, maxBody int) *Mail {
	m := &Mail{UID: uid}
	mr, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		m.Body = stripURLs(string(raw))
		return m
	}
	if s, err := mr.Header.Subject(); err == nil {
		m.Subject = s
	}
	if addrs, err := mr.Header.AddressList("From"); err == nil && len(addrs) > 0 {
		m.From = addrs[0].String()
	}
	if t, err := mr.Header.Date(); err == nil {
		m.Date = t.Format(time.RFC1123Z)
	}
	if id, err := mr.Header.MessageID(); err == nil {
		m.MessageID = "<" + id + ">"
	}

	var plain, html strings.Builder
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		} else if err != nil {
			break
		}
		switch h := p.Header.(type) {
		case *mail.InlineHeader:
			ct, _, _ := h.ContentType()
			body, _ := io.ReadAll(p.Body)
			switch {
			case ct == "text/plain":
				plain.Write(body)
				plain.WriteByte('\n')
			case ct == "text/html":
				html.Write(body)
				html.WriteByte('\n')
			case strings.HasPrefix(ct, "image/") && len(body) > 2048:
				m.Images = append(m.Images, body)
			}
		case *mail.AttachmentHeader:
			ct, _, _ := h.ContentType()
			if strings.HasPrefix(ct, "image/") {
				if body, _ := io.ReadAll(p.Body); len(body) > 2048 {
					m.Images = append(m.Images, body)
				}
			}
		}
	}
	body := strings.TrimSpace(plain.String())
	if body == "" {
		body = stripHTML(html.String())
	}
	body = stripURLs(body)
	if maxBody > 0 && len(body) > maxBody {
		body = body[:maxBody]
	}
	m.Body = body
	return m
}

func stripURLs(s string) string {
	return strings.TrimSpace(urlRe.ReplaceAllString(s, "[链接]"))
}
