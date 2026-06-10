// Package imap 负责 IMAP 取信、MIME 解析与 IDLE 守护（go-imap v2 + go-message）。
package imap

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"mime"
	"net"
	netmail "net/mail"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wangnov/mailpilot/internal/config"
	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	gomsgcharset "github.com/emersion/go-message/charset" // 注册 GBK/Big5/Shift-JIS… 解码器(init) + 提供 Reader
	"github.com/emersion/go-message/mail"
	"golang.org/x/net/html"
)

type Mail struct {
	UID       uint32
	From      string
	Subject   string
	Date      string
	MessageID string
	Body      string
	Images    [][]byte
	Mailbox   string   // 所在文件夹（复用邮箱侧信号：INBOX = 已过反垃圾过滤）
	Flags     []string // 标准 IMAP 标记，如 \Flagged(星标) \Answered(已回复)
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
		_ = tlsConn.Close()
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
	if b.cfg.Mailbox == "" { // 自动探测 \Junk(垃圾箱)特殊用途文件夹——跨语言/编码稳健
		junk, err := findSpecialUse(c, imap.MailboxAttrJunk)
		if err != nil || junk == "" {
			c.Close()
			return fmt.Errorf("未找到垃圾箱(\\Junk)文件夹: %v", err)
		}
		b.cfg.Mailbox = junk // 记录解析出的真实名字，供 mailbox_context 使用
	}
	if _, err := c.Select(b.cfg.Mailbox, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		c.Close()
		return err
	}
	b.c = c
	return nil
}

// findSpecialUse 通过 LIST(SPECIAL-USE) 找出带指定属性(如 \Junk)的文件夹名。
// go-imap 内部处理 modified-UTF-7，返回的名字可直接用于 Select。
func findSpecialUse(c *imapclient.Client, attr imap.MailboxAttr) (string, error) {
	data, err := c.List("", "*", &imap.ListOptions{ReturnSpecialUse: true}).Collect()
	if err != nil {
		return "", err
	}
	for _, d := range data {
		for _, a := range d.Attrs {
			if a == attr {
				return d.Mailbox, nil
			}
		}
	}
	return "", nil
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
	opts := &imap.FetchOptions{UID: true, Flags: true, BodySection: []*imap.FetchItemBodySection{bs}}
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
	m := parseMail(uid, buf.FindBodySection(bs), maxBody)
	if m != nil { // 复用邮箱侧信号
		m.Mailbox = b.cfg.Mailbox
		for _, f := range buf.Flags {
			m.Flags = append(m.Flags, string(f))
		}
	}
	return m, nil
}

// IdleLoop 进入 IDLE，新邮件触发 onNew；每 timeout 也跑一次 onNew 作周期性兜底
// （补 IDLE 可能漏掉的事件，并驱动垃圾箱扫描）。断连返回错误由外层重连。
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
		case <-timer.C:
		}
		idleCmd.Close()
		if err := idleCmd.Wait(); err != nil {
			return err
		}
		onNew() // 新邮件事件 或 周期超时都触发一次处理
	}
}

// ---- MIME 解析 ----

func stripHTML(h string) string {
	return strings.TrimSpace(strings.Join(htmlTokens(h, true), " "))
}

func htmlLinks(h string) []string {
	return htmlTokens(h, false)
}

func htmlTokens(h string, includeText bool) []string {
	z := html.NewTokenizer(strings.NewReader(h))
	var out []string
	var linkStack []string
	var skip string
	seenLinks := map[string]bool{}
	for {
		switch z.Next() {
		case html.ErrorToken:
			return out
		case html.StartTagToken:
			t := z.Token()
			name := strings.ToLower(t.Data)
			if name == "script" || name == "style" {
				skip = name
				continue
			}
			if name == "a" {
				href := ""
				for _, a := range t.Attr {
					if strings.EqualFold(a.Key, "href") {
						href = cleanHTTPURL(a.Val)
						break
					}
				}
				linkStack = append(linkStack, href)
				if !includeText && href != "" && !seenLinks[href] {
					out = append(out, href)
					seenLinks[href] = true
				}
			}
		case html.EndTagToken:
			t := z.Token()
			name := strings.ToLower(t.Data)
			if skip == name {
				skip = ""
				continue
			}
			if name == "a" && len(linkStack) > 0 {
				linkStack = linkStack[:len(linkStack)-1]
			}
		case html.TextToken:
			if skip != "" {
				continue
			}
			text := strings.TrimSpace(string(z.Text()))
			if text == "" {
				continue
			}
			text = strings.Join(strings.Fields(text), " ")
			href := ""
			if len(linkStack) > 0 {
				href = linkStack[len(linkStack)-1]
			}
			if includeText {
				if href != "" {
					out = append(out, text+" ("+href+")")
				} else {
					out = append(out, text)
				}
			} else if href != "" && !seenLinks[href] {
				out = append(out, href)
				seenLinks[href] = true
			}
		}
	}
}

func cleanHTTPURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !u.IsAbs() || u.Host == "" {
		return ""
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return u.String()
	default:
		return ""
	}
}

// fromWordDecoder 解码邮件头里的 RFC 2047 encoded-word（=?utf-8?q?…?= 之类），
// CharsetReader 复用 go-message/charset.Reader，故 GBK / Big5 / Shift-JIS 等非 UTF-8 字符集也能解。
var fromWordDecoder = &mime.WordDecoder{CharsetReader: gomsgcharset.Reader}

// decodeAddress 把 From 头解析成干净的「显示名 <地址>」。
// 为什么不直接用 go-message 的 AddressList()[0].String()：
//   - mail.Address 是 net/mail.Address 的别名，其 String() 会把非 ASCII 显示名【重新编码】
//     回 =?utf-8?b?…?=——推送通知里就显示成一串乱码；
//   - net/mail 对「单个 encoded-word 紧跟 <地址>」的解析有怪癖，会把编码字原样留下不解。
//
// 所以这里先把 encoded-word 解成明文（既消除上述怪癖，又兼容非 UTF-8），再解析地址、手工拼装，
// 全程不调用 .String()，从根上杜绝乱码。
func decodeAddress(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	dec, err := fromWordDecoder.DecodeHeader(raw)
	if err != nil || dec == "" {
		dec = raw
	}
	if a, err := netmail.ParseAddress(dec); err == nil {
		if a.Name != "" && a.Name != a.Address {
			return a.Name + " <" + a.Address + ">"
		}
		return a.Address
	}
	return dec // 解析失败也至少返回解码后的明文，绝不留 =?…?= 乱码给下游
}

func parseMail(uid uint32, raw []byte, maxBody int) *Mail {
	m := &Mail{UID: uid}
	mr, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		m.Body = clip(string(raw), maxBody) // 非 MIME 回退也要截断，防超大原文
		return m
	}
	if s, err := mr.Header.Subject(); err == nil {
		m.Subject = s
	}
	m.From = decodeAddress(mr.Header.Get("From"))
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
	htmlRaw := html.String()
	htmlBody := stripHTML(htmlRaw)
	if body == "" {
		body = htmlBody
	}
	m.Body = appendHTMLLinks(body, htmlRaw, maxBody) // 不再剥离 URL：工具受限(只读检索)，保留链接才能正确判别内容/真伪
	return m
}

func appendHTMLLinks(body, htmlRaw string, maxBytes int) string {
	links := htmlLinks(htmlRaw)
	if len(links) == 0 {
		return clip(body, maxBytes)
	}
	if maxBytes <= 0 {
		links = missingLinks(links, body)
		if len(links) == 0 {
			return body
		}
		linkBlock := "[HTML 链接]\n" + strings.Join(links, "\n")
		if strings.TrimSpace(body) == "" {
			return linkBlock
		}
		return body + "\n\n" + linkBlock
	}

	// 链接优先进入截断预算：纯文本里较晚出现的链接不能因为正文太长而丢失。
	linkBlock := boundedHTMLLinkBlock(links, maxBytes)
	if linkBlock == "" {
		return clip(body, maxBytes)
	}
	bodyBudget := remainingBodyBudget(maxBytes, linkBlock, strings.TrimSpace(body) != "")
	clippedBody := clipToBudget(body, bodyBudget)
	links = missingLinks(links, clippedBody)
	if len(links) == 0 {
		return clip(body, maxBytes)
	}
	linkBlock = boundedHTMLLinkBlock(links, maxBytes)
	if linkBlock == "" {
		return clip(body, maxBytes)
	}
	bodyBudget = remainingBodyBudget(maxBytes, linkBlock, strings.TrimSpace(body) != "")
	clippedBody = clipToBudget(body, bodyBudget)
	if strings.TrimSpace(clippedBody) == "" {
		return linkBlock
	}
	return clippedBody + "\n\n" + linkBlock
}

func missingLinks(links []string, body string) []string {
	var missing []string
	for _, link := range links {
		if !strings.Contains(body, link) {
			missing = append(missing, link)
		}
	}
	return missing
}

func boundedHTMLLinkBlock(links []string, maxBytes int) string {
	if len(links) == 0 {
		return ""
	}
	block := "[HTML 链接]"
	for _, link := range links {
		next := block + "\n" + link
		if maxBytes > 0 && len(next) > maxBytes {
			continue
		}
		block = next
	}
	if block == "[HTML 链接]" {
		return ""
	}
	return block
}

func remainingBodyBudget(maxBytes int, linkBlock string, hasBody bool) int {
	sepLen := 0
	if hasBody {
		sepLen = len("\n\n")
	}
	budget := maxBytes - len(linkBlock) - sepLen
	if budget < 0 {
		return 0
	}
	return budget
}

func clipToBudget(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	return clip(s, maxBytes)
}

// clip 按字节上限截断，并回退到合法 UTF-8 边界（不切碎多字节中文字符）。
func clip(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	s = s[:maxBytes]
	for len(s) > 0 {
		if r, size := utf8.DecodeLastRuneInString(s); r == utf8.RuneError && size <= 1 {
			s = s[:len(s)-1] // 去掉被截断的半个字符
		} else {
			break
		}
	}
	return s
}
