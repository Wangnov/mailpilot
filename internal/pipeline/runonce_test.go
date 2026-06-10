package pipeline

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wangnov/mailpilot/internal/analyze"
	"github.com/Wangnov/mailpilot/internal/config"
	"github.com/Wangnov/mailpilot/internal/imap"
	"github.com/Wangnov/mailpilot/internal/notify"
	"github.com/Wangnov/mailpilot/internal/state"
)

// ---- fakes ----

type fakeBox struct {
	uids      []uint32
	uidv      uint32
	err       error
	mailByUID map[uint32]*imap.Mail
}

func (f *fakeBox) Connect() error               { return nil }
func (f *fakeBox) Close()                       {}
func (f *fakeBox) UIDValidity() (uint32, error) { return f.uidv, f.err }
func (f *fakeBox) AllUIDs() ([]uint32, error)   { return append([]uint32(nil), f.uids...), nil }
func (f *fakeBox) Fetch(uid uint32, _ int) (*imap.Mail, error) {
	if f.mailByUID != nil {
		if m := f.mailByUID[uid]; m != nil {
			return m, nil
		}
	}
	return &imap.Mail{UID: uid, Subject: fmt.Sprintf("m%d", uid), From: "a@b.com", Mailbox: "INBOX"}, nil
}

type fakeProvider struct {
	a     *analyze.Analysis
	err   error
	byUID map[uint32]string // 可选：按 uid 返回不同分类
}

func (f *fakeProvider) Name() string        { return "fake" }
func (f *fakeProvider) SupportsTools() bool { return false }
func (f *fakeProvider) Analyze(m *imap.Mail, _ bool, _ string) (*analyze.Analysis, error) {
	if c, ok := f.byUID[m.UID]; ok {
		return okAnalysis(c), nil
	}
	return f.a, f.err
}

type fakeNotifier struct {
	sent []string
	err  error
	name string
}

func (f *fakeNotifier) Name() string {
	if f.name != "" {
		return f.name
	}
	return "fakeN"
}
func (f *fakeNotifier) Send(m notify.Message) error {
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, m.Title)
	return nil
}

type fakeOCR struct {
	called bool
	text   string
}

func (f *fakeOCR) Name() string { return "fakeOCR" }
func (f *fakeOCR) Images(_ [][]byte) string {
	f.called = true
	return f.text
}

func testPipe(t *testing.T, box mailbox, prov analyze.Provider, n *fakeNotifier, baseline bool, skip []string) *Pipeline {
	t.Helper()
	cfg := &config.Config{}
	cfg.Pipeline = config.Pipeline{BaselineOnFirstRun: baseline, MaxPerRun: 20, MaxRetry: 3, MaxBodyChars: 1000, SkipCategories: skip}
	statePath := filepath.Join(t.TempDir(), "s.json")
	cfg.Pipeline.StatePath = statePath
	st, err := state.Load(statePath)
	if err != nil {
		t.Fatal(err)
	}
	return &Pipeline{
		cfg: cfg, log: func(string) {}, box: box,
		providers: []analyze.Provider{prov}, notifiers: []notify.Notifier{n},
		st: st, pace: 0,
	}
}

func okAnalysis(cat string) *analyze.Analysis {
	return &analyze.Analysis{Category: cat, Urgency: "中", Summary: "s"}
}

// ---- tests ----

func TestRunOnceProcessAndPush(t *testing.T) {
	box := &fakeBox{uids: []uint32{1, 2, 3}, uidv: 7}
	n := &fakeNotifier{}
	p := testPipe(t, box, &fakeProvider{a: okAnalysis("工作")}, n, false, nil)
	if err := p.RunOnce(); err != nil {
		t.Fatal(err)
	}
	if len(n.sent) != 3 {
		t.Errorf("pushed %d, want 3", len(n.sent))
	}
	if p.st.LastUID != 3 {
		t.Errorf("watermark=%d, want 3", p.st.LastUID)
	}
	if len(p.st.Failed) != 0 {
		t.Errorf("failed should be empty: %v", p.st.Failed)
	}
}

func TestRunOnceBaselineThenProcess(t *testing.T) {
	box := &fakeBox{uids: []uint32{1, 2, 3}, uidv: 7}
	n := &fakeNotifier{}
	p := testPipe(t, box, &fakeProvider{a: okAnalysis("工作")}, n, true, nil)
	if err := p.RunOnce(); err != nil { // 首跑：建基线，不推
		t.Fatal(err)
	}
	if len(n.sent) != 0 || !p.st.BaselineDone || p.st.LastUID != 3 {
		t.Fatalf("baseline run wrong: sent=%d done=%v last=%d", len(n.sent), p.st.BaselineDone, p.st.LastUID)
	}
	box.uids = append(box.uids, 4) // 来一封新的
	if err := p.RunOnce(); err != nil {
		t.Fatal(err)
	}
	if len(n.sent) != 1 || p.st.LastUID != 4 {
		t.Errorf("second run: sent=%d (want 1), last=%d (want 4)", len(n.sent), p.st.LastUID)
	}
}

func TestRunOnceSkipCategory(t *testing.T) {
	box := &fakeBox{uids: []uint32{1}, uidv: 7}
	n := &fakeNotifier{}
	p := testPipe(t, box, &fakeProvider{a: okAnalysis("垃圾")}, n, false, []string{"垃圾", "营销推广"})
	if err := p.RunOnce(); err != nil {
		t.Fatal(err)
	}
	if len(n.sent) != 0 {
		t.Errorf("skip 分类不应推送，但推了 %d", len(n.sent))
	}
	if p.st.LastUID != 1 || len(p.st.Failed) != 0 {
		t.Errorf("skip 应算处理完成、水位推进: last=%d failed=%v", p.st.LastUID, p.st.Failed)
	}
}

func TestRunOnceDroppableGivesUp(t *testing.T) {
	box := &fakeBox{uids: []uint32{1}, uidv: 7}
	n := &fakeNotifier{}
	p := testPipe(t, box, &fakeProvider{err: analyze.DroppableErr(errors.New("坏 JSON"))}, n, false, nil)
	for i := 0; i < 3; i++ { // MaxRetry=3：第 3 次后放弃
		if err := p.RunOnce(); err != nil {
			t.Fatal(err)
		}
	}
	if len(n.sent) != 0 {
		t.Error("失败邮件不应被推送")
	}
	if len(p.st.Failed) != 0 {
		t.Errorf("达上限应放弃(从队列移除)，但仍有: %v", p.st.Failed)
	}
}

func TestRunOnceTransientNeverDrops(t *testing.T) {
	box := &fakeBox{uids: []uint32{1}, uidv: 7}
	n := &fakeNotifier{}
	p := testPipe(t, box, &fakeProvider{err: analyze.TransientErr(errors.New("网络抖动"))}, n, false, nil)
	for i := 0; i < 6; i++ { // 远超 MaxRetry：暂时性故障绝不放弃
		if err := p.RunOnce(); err != nil {
			t.Fatal(err)
		}
	}
	if c, ok := p.st.Failed["1"]; !ok || c >= p.cfg.Pipeline.MaxRetry {
		t.Errorf("暂时性故障应一直保留重试，failed[1]=%d ok=%v", c, ok)
	}
	if len(n.sent) != 0 {
		t.Error("一直失败不应推送")
	}
}

func TestRunOnceSpamRescue(t *testing.T) {
	inbox := &fakeBox{uidv: 1}                      // 收件箱空
	spam := &fakeBox{uids: []uint32{1, 2}, uidv: 1} // 垃圾箱里 2 封
	n := &fakeNotifier{}
	// uid1 LLM 判为「工作」(=误判进垃圾箱，应救回)；uid2 判为「垃圾」(=确属垃圾，不打扰)
	prov := &fakeProvider{byUID: map[uint32]string{1: "工作", 2: "垃圾"}}
	p := testPipe(t, inbox, prov, n, false, nil)
	p.spamBox = spam
	if err := p.RunOnce(); err != nil {
		t.Fatal(err)
	}
	if len(n.sent) != 1 {
		t.Fatalf("救援应只推 1 封(被误判的)，实际 %d", len(n.sent))
	}
	if !strings.Contains(n.sent[0], "可能误判") {
		t.Errorf("救回的推送应带误判标记，实际标题: %q", n.sent[0])
	}
	if p.st.SpamLastUID != 2 {
		t.Errorf("垃圾箱水位应推进到 2，实际 %d", p.st.SpamLastUID)
	}
	if p.st.LastUID != 0 {
		t.Errorf("收件箱空，水位不应动，实际 %d", p.st.LastUID)
	}
}

func TestRunOnceNotifyFailureRetries(t *testing.T) {
	box := &fakeBox{uids: []uint32{1}, uidv: 7}
	n := &fakeNotifier{err: errors.New("bark 挂了")}
	p := testPipe(t, box, &fakeProvider{a: okAnalysis("工作")}, n, false, nil)
	for i := 0; i < 5; i++ {
		if err := p.RunOnce(); err != nil {
			t.Fatal(err)
		}
	}
	// 通知失败属暂时性：邮件应一直留在重试队列，不被放弃
	if c, ok := p.st.Failed["1"]; !ok || c >= p.cfg.Pipeline.MaxRetry {
		t.Errorf("通知失败应保留重试，failed[1]=%d ok=%v", c, ok)
	}
}

func TestProcessOneIgnoresHTMLLinkBlockForOCRThreshold(t *testing.T) {
	box := &fakeBox{
		uids: []uint32{1}, uidv: 7,
		mailByUID: map[uint32]*imap.Mail{
			1: {
				UID: 1, Subject: "image button", From: "a@b.com", Mailbox: "INBOX",
				Body:   "[HTML 链接]\nhttps://example.com/confirm?token=abc",
				Images: [][]byte{[]byte(strings.Repeat("x", 2049))},
			},
		},
	}
	n := &fakeNotifier{}
	p := testPipe(t, box, &fakeProvider{a: okAnalysis("工作")}, n, false, nil)
	p.cfg.OCR.Enabled = true
	p.cfg.OCR.MinBody = 30
	ocr := &fakeOCR{text: "图片验证码 294817"}
	p.ocrEngine = ocr

	if err := p.processOne(1, p.inboxTarget()); err != nil {
		t.Fatal(err)
	}
	if !ocr.called {
		t.Fatal("OCR should run when body only contains preserved HTML links")
	}
}

func TestProcessOneIgnoresInlinePreservedURLForOCRThreshold(t *testing.T) {
	box := &fakeBox{
		uids: []uint32{1}, uidv: 7,
		mailByUID: map[uint32]*imap.Mail{
			1: {
				UID: 1, Subject: "image link", From: "a@b.com", Mailbox: "INBOX",
				Body:   "确认 (https://example.com/confirm?token=" + strings.Repeat("a", 80) + ")",
				Images: [][]byte{[]byte(strings.Repeat("x", 2049))},
			},
		},
	}
	n := &fakeNotifier{}
	p := testPipe(t, box, &fakeProvider{a: okAnalysis("工作")}, n, false, nil)
	p.cfg.OCR.Enabled = true
	p.cfg.OCR.MinBody = 30
	ocr := &fakeOCR{text: "图片验证码 294817"}
	p.ocrEngine = ocr

	if err := p.processOne(1, p.inboxTarget()); err != nil {
		t.Fatal(err)
	}
	if !ocr.called {
		t.Fatal("OCR should run when body only has short link text plus a preserved URL")
	}
}

func TestRunOnceUIDValidityErrorDoesNotBaseline(t *testing.T) {
	box := &fakeBox{uids: []uint32{1, 2, 3}, err: errors.New("status failed")}
	n := &fakeNotifier{}
	p := testPipe(t, box, &fakeProvider{a: okAnalysis("工作")}, n, true, nil)
	if err := p.RunOnce(); err == nil {
		t.Fatal("UIDValidity error should stop the run")
	}
	if p.st.BaselineDone || p.st.LastUID != 0 {
		t.Fatalf("state should not be baselined on UIDValidity error: %+v", p.st)
	}
}

func TestRunOncePartialNotifyRetriesOnlyFailedChannel(t *testing.T) {
	box := &fakeBox{uids: []uint32{1}, uidv: 7}
	okN := &fakeNotifier{name: "ok"}
	flaky := &fakeNotifier{name: "flaky", err: errors.New("down")}
	p := testPipe(t, box, &fakeProvider{a: okAnalysis("工作")}, okN, false, nil)
	p.notifiers = []notify.Notifier{okN, flaky}

	if err := p.RunOnce(); err != nil {
		t.Fatal(err)
	}
	if len(okN.sent) != 1 {
		t.Fatalf("first run should send ok channel once, got %d", len(okN.sent))
	}
	if _, ok := p.st.Delivered["1"]["00:ok"]; !ok {
		t.Fatalf("successful channel should be persisted: %+v", p.st.Delivered)
	}

	flaky.err = nil
	if err := p.RunOnce(); err != nil {
		t.Fatal(err)
	}
	if len(okN.sent) != 1 {
		t.Fatalf("ok channel should not be duplicated, got %d sends", len(okN.sent))
	}
	if len(p.st.Failed) != 0 || len(p.st.Delivered) != 0 {
		t.Fatalf("success should clear retry state: failed=%v delivered=%v", p.st.Failed, p.st.Delivered)
	}
}

func TestNotifyPendingPersistsBeforeWatermarkAdvance(t *testing.T) {
	box := &fakeBox{uids: []uint32{1}, uidv: 7}
	n1 := &fakeNotifier{name: "one"}
	n2 := &fakeNotifier{name: "two"}
	p := testPipe(t, box, &fakeProvider{a: okAnalysis("工作")}, n1, false, nil)
	p.notifiers = []notify.Notifier{n1, n2}

	err := p.notifyPending(p.inboxTarget(), 1, &imap.Mail{UID: 1, Subject: "m1"}, okAnalysis("工作"))
	if err != nil {
		t.Fatal(err)
	}
	saved, err := state.Load(p.cfg.Pipeline.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if !saved.Delivered["1"]["00:one"] || !saved.Delivered["1"]["01:two"] {
		t.Fatalf("all successful channels should be durable before watermark advance: %+v", saved.Delivered)
	}
}
