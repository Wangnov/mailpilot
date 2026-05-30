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
	// 项目目录 = 配置文件所在目录；codex 临时产物只落在它下面（详见 analyze.BuildProvider）。
	abs, _ := filepath.Abs(configPath)
	workDir := filepath.Dir(abs)
	var providers []analyze.Provider
	for _, pc := range cfg.Analyze.Providers {
		p, err := analyze.BuildProvider(pc, cfg.Analyze.Timeout, workDir, cfg.Analyze.Language)
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
	if skipCategory(a.Category, p.cfg.Pipeline.SkipCategories) {
		p.log(fmt.Sprintf("· uid=%d 分类[%s] 命中忽略规则，跳过推送", uid, a.Category))
		return nil // 已成功分析、仅按规则不推送：算处理完成，水位线照常推进
	}
	if !notify.NotifyAll(p.notifiers, mail, a, p.log) {
		return fmt.Errorf("部分通知渠道失败")
	}
	p.log(fmt.Sprintf("✓ uid=%d 已推送 [%s/%s]", uid, a.Category, a.Urgency))
	return nil
}

// skipCategory 判断某分类是否在「只分析不推送」忽略名单内。
func skipCategory(category string, skip []string) bool {
	for _, s := range skip {
		if s == category {
			return true
		}
	}
	return false
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
	todo, newCount, retryCount, total := planTodo(
		all, allSet, last, p.st.Failed, p.cfg.Pipeline.MaxPerRun, p.cfg.Pipeline.MaxRetry)
	if total == 0 {
		p.log("无新邮件。")
		return nil
	}
	if total > p.cfg.Pipeline.MaxPerRun {
		p.log(fmt.Sprintf("待处理 %d 封超上限 %d，本次先处理最旧 %d 封，其余下轮继续。",
			total, p.cfg.Pipeline.MaxPerRun, p.cfg.Pipeline.MaxPerRun))
	}
	p.log(fmt.Sprintf("待处理 %d 封（新 %d / 重试 %d）", len(todo), newCount, retryCount))

	// 携带失败计数前推，顺手剔除已不在邮箱内(被删)的死条目。
	failed := make(map[string]int, len(p.st.Failed))
	for k, v := range p.st.Failed {
		if vv, e := strconv.ParseUint(k, 10, 32); e == nil && allSet[uint32(vv)] {
			failed[k] = v
		}
	}
	for _, uid := range todo {
		key := strconv.Itoa(int(uid))
		if err := p.processOne(uid); err != nil {
			failed[key]++
			if failed[key] >= p.cfg.Pipeline.MaxRetry {
				p.log(fmt.Sprintf("✗ uid=%d 第%d次失败，已达上限放弃: %s", uid, failed[key], err.Error()))
				delete(failed, key) // 放弃重试，避免死条目无限堆积
			} else {
				p.log(fmt.Sprintf("✗ uid=%d 处理失败(第%d次，将重试): %s", uid, failed[key], err.Error()))
			}
		} else {
			delete(failed, key)
		}
		time.Sleep(300 * time.Millisecond)
	}

	// 水位线只推进到本轮 todo 里实际覆盖的最大 uid（retry 的旧 uid < last 不影响）。
	p.st.LastUID = advanceWatermark(todo, last)
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

// planTodo 计算本轮待处理 uid：新邮件(uid>last) ∪ 可重试的失败 uid，升序排列、
// 最旧优先(FIFO)、截断到 maxPerRun。total 是未截断前的总数(供日志)。
// 截断保留【最旧】的 N 封，配合 advanceWatermark 保证超限的较新邮件留待下轮、绝不丢弃。
func planTodo(all []uint32, allSet map[uint32]bool, last uint32, failed map[string]int, maxPerRun, maxRetry int) (todo []uint32, newCount, retryCount, total int) {
	var newUIDs, retry []uint32
	for _, u := range all {
		if u > last {
			newUIDs = append(newUIDs, u)
		}
	}
	for k, c := range failed {
		if c < maxRetry {
			if v, e := strconv.ParseUint(k, 10, 32); e == nil && allSet[uint32(v)] {
				retry = append(retry, uint32(v))
			}
		}
	}
	todo = mergeSorted(newUIDs, retry)
	total = len(todo)
	if maxPerRun > 0 && total > maxPerRun {
		todo = todo[:maxPerRun] // 最旧优先
	}
	return todo, len(newUIDs), len(retry), total
}

// advanceWatermark 把水位线推进到 todo 内最大的 uid（但不低于原 last）。
func advanceWatermark(todo []uint32, last uint32) uint32 {
	nm := last
	for _, u := range todo {
		if u > nm {
			nm = u
		}
	}
	return nm
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
