// Package pipeline 编排：取信 → OCR → 预检测 → 分析(降级) → 通知 → 去重/重试。
package pipeline

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/Wangnov/mailpilot/internal/analyze"
	"github.com/Wangnov/mailpilot/internal/config"
	"github.com/Wangnov/mailpilot/internal/imap"
	"github.com/Wangnov/mailpilot/internal/notify"
	"github.com/Wangnov/mailpilot/internal/ocr"
	"github.com/Wangnov/mailpilot/internal/state"
)

// ToolCmd 是 agentic provider 在沙箱里调用的历史检索命令。
const ToolCmd = "mailpilot tool-search"

type Pipeline struct {
	cfg       *config.Config
	log       func(string)
	box       *imap.Box
	providers []analyze.Provider
	notifiers []notify.Notifier
	st        *state.State
}

func New(cfg *config.Config, configPath string, log func(string)) (*Pipeline, error) {
	abs, _ := filepath.Abs(configPath)
	workdir := filepath.Dir(abs)
	var providers []analyze.Provider
	for _, pc := range cfg.Analyze.Providers {
		p, err := analyze.BuildProvider(pc, cfg.Analyze.Timeout, workdir)
		if err != nil {
			return nil, err
		}
		providers = append(providers, p)
	}
	var notifiers []notify.Notifier
	for _, nc := range cfg.Notify {
		n, err := notify.BuildNotifier(nc)
		if err != nil {
			return nil, err
		}
		notifiers = append(notifiers, n)
	}
	return &Pipeline{
		cfg: cfg, log: log, box: imap.New(cfg.IMAP),
		providers: providers, notifiers: notifiers,
		st: state.Load(cfg.Pipeline.StatePath),
	}, nil
}

var (
	reFwd = regexp.MustCompile(`(?i)^\s*(re|fwd|fw)\s*[:：]`)
	reZh  = regexp.MustCompile(`(答复|转发)[:：]`)
	reIss = regexp.MustCompile(`(?i)(#\d{1,7}\b|issue|pull request|\bPR\b|工单|ticket)`)
)

func likelyHasHistory(m *imap.Mail) bool {
	if reFwd.MatchString(m.Subject) || reZh.MatchString(m.Subject) {
		return true
	}
	return reIss.MatchString(m.Subject + " " + m.Body)
}

func (p *Pipeline) processOne(uid uint32) error {
	mail, err := p.box.Fetch(uid, p.cfg.Pipeline.MaxBodyChars)
	if err != nil {
		return err
	}
	if mail == nil {
		p.log(fmt.Sprintf("uid=%d 取信失败(可能已删)，跳过", uid))
		return nil
	}
	if p.cfg.OCR.Enabled && len([]rune(mail.Body)) < p.cfg.OCR.MinBody && len(mail.Images) > 0 {
		if t := ocr.Images(mail.Images, p.cfg.OCR, p.log); t != "" {
			mail.Body = "[此邮件正文主要为图片，以下为 OCR 识别结果]\n" + t
		}
	}
	hist := p.cfg.Pipeline.HistorySearch && likelyHasHistory(mail)
	p.log(fmt.Sprintf("分析 uid=%d | %s | 历史检索:%s", uid, truncRune(mail.Subject, 40), onoff(hist)))
	a, err := analyze.WithFallback(p.providers, mail, hist, ToolCmd, p.log)
	if err != nil {
		return err
	}
	if !notify.NotifyAll(p.notifiers, mail, a, p.log) {
		return fmt.Errorf("部分通知渠道失败")
	}
	p.log(fmt.Sprintf("✓ uid=%d 已推送 [%s/%s]", uid, a.Category, a.Urgency))
	return nil
}

func (p *Pipeline) RunOnce() error {
	if err := p.box.Connect(); err != nil {
		return err
	}
	defer p.box.Close()

	uidv, _ := p.box.UIDValidity()
	all, err := p.box.AllUIDs()
	if err != nil {
		return err
	}
	var maxUID uint32
	allSet := make(map[uint32]bool, len(all))
	for _, u := range all {
		allSet[u] = true
		if u > maxUID {
			maxUID = u
		}
	}

	if p.cfg.Pipeline.BaselineOnFirstRun && (!p.st.BaselineDone || p.st.UIDValidity != uidv) {
		p.st.SetBaseline(uidv, maxUID)
		_ = p.st.Save()
		p.log(fmt.Sprintf("基线已建立：共 %d 封，水位 last_uid=%d，本次不推历史。", len(all), maxUID))
		return nil
	}

	last := p.st.LastUID
	var newUIDs []uint32
	for _, u := range all {
		if u > last {
			newUIDs = append(newUIDs, u)
		}
	}
	var retry []uint32
	for k, c := range p.st.Failed {
		if c < p.cfg.Pipeline.MaxRetry {
			if v, e := strconv.ParseUint(k, 10, 32); e == nil && allSet[uint32(v)] {
				retry = append(retry, uint32(v))
			}
		}
	}
	todo := mergeSorted(newUIDs, retry)
	if len(todo) == 0 {
		p.log("无新邮件。")
		return nil
	}
	if len(todo) > p.cfg.Pipeline.MaxPerRun {
		p.log(fmt.Sprintf("待处理 %d 封超上限 %d，本次先处理最新 %d 封。",
			len(todo), p.cfg.Pipeline.MaxPerRun, p.cfg.Pipeline.MaxPerRun))
		todo = todo[len(todo)-p.cfg.Pipeline.MaxPerRun:]
	}
	p.log(fmt.Sprintf("待处理 %d 封（新 %d / 重试 %d）", len(todo), len(newUIDs), len(retry)))

	failed := make(map[string]int, len(p.st.Failed))
	for k, v := range p.st.Failed {
		failed[k] = v
	}
	for _, uid := range todo {
		key := strconv.Itoa(int(uid))
		if err := p.processOne(uid); err != nil {
			failed[key]++
			p.log(fmt.Sprintf("✗ uid=%d 处理失败(第%d次): %s", uid, failed[key], err.Error()))
		} else {
			delete(failed, key)
		}
		time.Sleep(300 * time.Millisecond)
	}

	newMax := last
	for _, u := range newUIDs {
		if u > newMax {
			newMax = u
		}
	}
	p.st.LastUID = newMax
	p.st.Failed = failed
	p.st.UIDValidity = uidv
	_ = p.st.Save()
	p.log(fmt.Sprintf("本轮完成。水位 last_uid=%d，待重试 %d 封。", p.st.LastUID, len(failed)))
	return nil
}

// Daemon 常驻 IMAP IDLE，新邮件秒级触发；断连自动重连。
func (p *Pipeline) Daemon() error {
	listen := imap.New(p.cfg.IMAP)
	guarded := func() {
		if err := p.RunOnce(); err != nil {
			p.log("处理异常: " + err.Error())
		}
	}
	for {
		if err := listen.Connect(); err != nil {
			p.log("IMAP 连接失败，10s 后重试: " + err.Error())
			time.Sleep(10 * time.Second)
			continue
		}
		p.log("已连接 IMAP，进入 IDLE 守护")
		guarded() // 启动 catch-up
		err := listen.IdleLoop(guarded, time.Duration(p.cfg.Pipeline.IdleTimeout)*time.Second)
		listen.Close()
		msg := "IDLE 结束"
		if err != nil {
			msg = "IDLE 断连: " + err.Error()
		}
		p.log(msg + "，10s 后重连")
		time.Sleep(10 * time.Second)
	}
}

func mergeSorted(a, b []uint32) []uint32 {
	s := map[uint32]bool{}
	for _, x := range a {
		s[x] = true
	}
	for _, x := range b {
		s[x] = true
	}
	out := make([]uint32, 0, len(s))
	for x := range s {
		out = append(out, x)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func onoff(b bool) string {
	if b {
		return "开"
	}
	return "关"
}

func truncRune(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
