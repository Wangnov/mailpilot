// Package pipeline 编排：取信 → OCR → 预检测 → 分析(降级) → 通知 → 去重/重试。
package pipeline

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
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

// mailbox 抽象出 RunOnce 用到的只读取信能力，便于用假实现做集成测试。*imap.Box 实现它。
type mailbox interface {
	Connect() error
	Close()
	UIDValidity() (uint32, error)
	AllUIDs() ([]uint32, error)
	Fetch(uid uint32, maxBody int) (*imap.Mail, error)
}

// scanTarget 描述一次扫描的对象：用哪个 mailbox、读写 state 里的哪组水位线/重试队列，
// 以及是否走"垃圾箱救援"模式。INBOX 与垃圾箱共用同一套扫描逻辑(scanOnce)，只是指向不同字段。
type scanTarget struct {
	box          mailbox
	uidv         *uint32
	lastUID      *uint32
	baselineDone *bool
	failed       *map[string]int
	delivered    *state.DeliveryMap
	rescue       bool   // 垃圾箱：仅当 LLM 判定它【不是】垃圾/营销时才推送(救回误判)
	label        string // 日志前缀
}

type Pipeline struct {
	cfg       *config.Config
	log       func(string)
	box       mailbox
	spamBox   mailbox // 兜底扫描垃圾箱；scan_spam 关闭时为 nil
	providers []analyze.Provider
	notifiers []notify.Notifier
	ocrEngine ocr.Engine
	st        *state.State
	pace      time.Duration // 每封之间的间隔（默认 300ms，测试可设 0）
}

func New(cfg *config.Config, configPath string, log func(string)) (*Pipeline, error) {
	if log == nil {
		log = func(string) {}
	}
	if cfg == nil {
		return nil, fmt.Errorf("配置为空")
	}
	// 项目目录 = 配置文件所在目录；codex 临时产物只落在它下面（详见 analyze.BuildProvider）。
	abs, _ := filepath.Abs(configPath)
	workDir := filepath.Dir(abs)
	if len(cfg.Analyze.Providers) == 0 {
		return nil, fmt.Errorf("至少需要配置一个 analyze provider")
	}
	var providers []analyze.Provider
	for _, pc := range cfg.Analyze.Providers {
		p, err := analyze.BuildProvider(pc, cfg.Analyze.Timeout, workDir, cfg.Analyze.Language)
		if err != nil {
			return nil, err
		}
		providers = append(providers, p)
	}
	if len(cfg.Notify) == 0 {
		return nil, fmt.Errorf("至少需要配置一个 notify 渠道")
	}
	var notifiers []notify.Notifier
	for _, nc := range cfg.Notify {
		n, err := notify.BuildNotifier(nc)
		if err != nil {
			return nil, err
		}
		notifiers = append(notifiers, n)
	}
	ocrEngine, err := ocr.Build(cfg.OCR, log)
	if err != nil {
		return nil, err
	}
	if cfg.Pipeline.StatePath == "" {
		cfg.Pipeline.StatePath = "state.json"
	}
	if cfg.Pipeline.StatePath != "" && !filepath.IsAbs(cfg.Pipeline.StatePath) {
		cfg.Pipeline.StatePath = filepath.Join(workDir, cfg.Pipeline.StatePath)
	}
	p := &Pipeline{
		cfg: cfg, log: log, box: imap.New(cfg.IMAP),
		providers: providers, notifiers: notifiers, ocrEngine: ocrEngine,
		pace: 300 * time.Millisecond,
	}
	p.st, err = state.Load(cfg.Pipeline.StatePath)
	if err != nil {
		return nil, err
	}
	if cfg.Pipeline.ScanSpam { // 兜底扫垃圾箱：单独连接；mailbox 留空则 Connect 自动探测 \Junk
		spamCfg := cfg.IMAP
		spamCfg.Mailbox = cfg.IMAP.SpamMailbox
		p.spamBox = imap.New(spamCfg)
	}
	return p, nil
}

var (
	reFwd              = regexp.MustCompile(`(?i)^\s*(re|fwd|fw)\s*[:：]`)
	reZh               = regexp.MustCompile(`(答复|转发)[:：]`)
	reIss              = regexp.MustCompile(`(?i)(#\d{1,7}\b|issue|pull request|\bPR\b|工单|ticket)`)
	rePreservedLinkURL = regexp.MustCompile(`\s+\(https?://[^)\s]+(?:\s+[^)]*)?\)`)
)

func likelyHasHistory(m *imap.Mail) bool {
	if reFwd.MatchString(m.Subject) || reZh.MatchString(m.Subject) {
		return true
	}
	return reIss.MatchString(m.Subject + " " + m.Body)
}

func (p *Pipeline) processOne(uid uint32, tgt scanTarget) error {
	mail, err := tgt.box.Fetch(uid, p.cfg.Pipeline.MaxBodyChars)
	if err != nil {
		return err
	}
	if mail == nil {
		p.log(fmt.Sprintf("%suid=%d 取信失败(可能已删)，跳过", tgt.label, uid))
		return nil
	}
	if p.ocrEngine != nil && p.cfg.OCR.Enabled && len([]rune(bodyForOCRThreshold(mail.Body))) < p.cfg.OCR.MinBody && len(mail.Images) > 0 {
		if t := p.ocrEngine.Images(mail.Images); t != "" {
			if strings.TrimSpace(mail.Body) == "" {
				mail.Body = "[此邮件正文主要为图片，以下为 OCR 识别结果]\n" + t
			} else {
				mail.Body = mail.Body + "\n\n[OCR 识别结果]\n" + t
			}
		}
	}
	hist := p.cfg.Pipeline.HistorySearch && likelyHasHistory(mail)
	p.log(fmt.Sprintf("%s分析 uid=%d | %s | 历史检索:%s", tgt.label, uid, truncRune(mail.Subject, 40), onoff(hist)))
	a, err := analyze.WithFallback(p.providers, mail, hist, ToolCmd, p.log)
	if err != nil {
		return err
	}
	if tgt.rescue {
		// 垃圾箱救援：LLM 同意它是垃圾/营销 → 不打扰；否则视为误判，标注后推送。
		if a.Category == "垃圾" || a.Category == "营销推广" {
			p.log(fmt.Sprintf("%suid=%d 经判定确为[%s]，不打扰", tgt.label, uid, a.Category))
			return nil
		}
		mail.Subject = "[可能误判·垃圾箱] " + mail.Subject
	} else if skipCategory(a.Category, p.cfg.Pipeline.SkipCategories) {
		p.log(fmt.Sprintf("· uid=%d 分类[%s] 命中忽略规则，跳过推送", uid, a.Category))
		return nil // 已成功分析、仅按规则不推送：算处理完成，水位线照常推进
	}
	if err := p.notifyPending(tgt, uid, mail, a); err != nil {
		return err
	}
	p.log(fmt.Sprintf("%s✓ uid=%d 已推送 [%s/%s]", tgt.label, uid, a.Category, a.Urgency))
	return nil
}

func (p *Pipeline) notifyPending(tgt scanTarget, uid uint32, mail *imap.Mail, a *analyze.Analysis) error {
	m := notify.BuildMessage(mail, a)
	key := strconv.Itoa(int(uid))
	delivered := (*tgt.delivered)[key]
	if delivered == nil {
		delivered = map[string]bool{}
	}
	(*tgt.delivered)[key] = delivered
	ok := true
	for i, n := range p.notifiers {
		nk := notifierKey(i, n)
		if delivered[nk] {
			continue
		}
		if err := n.Send(m); err != nil {
			ok = false
			p.log(fmt.Sprintf("通知渠道 %s 失败: %s", n.Name(), err.Error()))
			continue
		}
		delivered[nk] = true
		if err := p.st.Save(); err != nil {
			return err
		}
	}
	if !ok {
		return fmt.Errorf("部分通知渠道失败")
	}
	return nil
}

func bodyForOCRThreshold(body string) string {
	const marker = "[HTML 链接]\n"
	trimmed := strings.TrimSpace(body)
	if strings.HasPrefix(trimmed, marker) {
		return ""
	}
	if i := strings.Index(body, "\n\n"+marker); i >= 0 {
		return bodyTextForOCRThreshold(body[:i])
	}
	return bodyTextForOCRThreshold(body)
}

func bodyTextForOCRThreshold(body string) string {
	return strings.TrimSpace(rePreservedLinkURL.ReplaceAllString(body, ""))
}

func notifierKey(i int, n notify.Notifier) string {
	return fmt.Sprintf("%02d:%s", i, n.Name())
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

func (p *Pipeline) inboxTarget() scanTarget {
	return scanTarget{box: p.box, uidv: &p.st.UIDValidity, lastUID: &p.st.LastUID,
		baselineDone: &p.st.BaselineDone, failed: &p.st.Failed, delivered: &p.st.Delivered}
}

func (p *Pipeline) spamTarget() scanTarget {
	return scanTarget{box: p.spamBox, uidv: &p.st.SpamUIDValidity, lastUID: &p.st.SpamLastUID,
		baselineDone: &p.st.SpamBaselineDone, failed: &p.st.SpamFailed, delivered: &p.st.SpamDelivered, rescue: true, label: "[垃圾箱] "}
}

// RunOnce 扫一遍收件箱；若开启 scan_spam，再兜底扫一遍垃圾箱(救回误判)。垃圾箱失败不影响主流程。
func (p *Pipeline) RunOnce() error {
	lock, err := state.AcquireLock(p.cfg.Pipeline.StatePath)
	if err != nil {
		return err
	}
	defer lock.Close()
	p.st, err = state.Load(p.cfg.Pipeline.StatePath)
	if err != nil {
		return err
	}
	if err := p.scanOnce(p.inboxTarget()); err != nil {
		return err
	}
	if p.spamBox != nil {
		if err := p.scanOnce(p.spamTarget()); err != nil {
			p.log("[垃圾箱] 扫描异常: " + err.Error())
		}
	}
	return p.st.Save()
}

// scanOnce 对单个 mailbox 执行：连接 → 首跑建基线 → 选取待处理 → 分析推送 → 更新水位/重试队列。
func (p *Pipeline) scanOnce(tgt scanTarget) error {
	if err := tgt.box.Connect(); err != nil {
		return err
	}
	defer tgt.box.Close()

	uidv, err := tgt.box.UIDValidity()
	if err != nil {
		return err
	}
	all, err := tgt.box.AllUIDs()
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

	if p.cfg.Pipeline.BaselineOnFirstRun && (!*tgt.baselineDone || *tgt.uidv != uidv) {
		*tgt.uidv, *tgt.lastUID, *tgt.baselineDone, *tgt.failed = uidv, maxUID, true, map[string]int{}
		*tgt.delivered = state.DeliveryMap{}
		p.log(fmt.Sprintf("%s基线已建立：共 %d 封，水位 last_uid=%d，本次不推历史。", tgt.label, len(all), maxUID))
		return nil
	}

	todo, newCount, retryCount, total := planTodo(
		all, allSet, *tgt.lastUID, *tgt.failed, p.cfg.Pipeline.MaxPerRun, p.cfg.Pipeline.MaxRetry)
	if total == 0 {
		return nil // 无新邮件：静默返回（daemon 会周期性扫描，避免刷屏）
	}
	if total > p.cfg.Pipeline.MaxPerRun {
		p.log(fmt.Sprintf("%s待处理 %d 封超上限 %d，本次先处理最旧 %d 封，其余下轮继续。",
			tgt.label, total, p.cfg.Pipeline.MaxPerRun, p.cfg.Pipeline.MaxPerRun))
	}
	p.log(fmt.Sprintf("%s待处理 %d 封（新 %d / 重试 %d）", tgt.label, len(todo), newCount, retryCount))

	// 携带失败计数前推，顺手剔除已不在邮箱内(被删)的死条目。
	failed := make(map[string]int, len(*tgt.failed))
	for k, v := range *tgt.failed {
		if vv, e := strconv.ParseUint(k, 10, 32); e == nil && allSet[uint32(vv)] {
			failed[k] = v
		}
	}
	pruneDeliveryMap(*tgt.delivered, allSet)
	for _, uid := range todo {
		key := strconv.Itoa(int(uid))
		err := p.processOne(uid, tgt)
		switch {
		case err == nil:
			delete(failed, key)
			delete(*tgt.delivered, key)
		case analyze.IsDroppable(err):
			// 模型已响应但产物无法解析——这封邮件本身有问题，多次后放弃。
			failed[key]++
			if failed[key] >= p.cfg.Pipeline.MaxRetry {
				p.log(fmt.Sprintf("%s✗ uid=%d 第%d次失败，已达上限放弃: %s", tgt.label, uid, failed[key], err.Error()))
				delete(failed, key)
			} else {
				p.log(fmt.Sprintf("%s✗ uid=%d 处理失败(第%d次，将重试): %s", tgt.label, uid, failed[key], err.Error()))
			}
		default:
			// 暂时性故障（网络/限流/exec/通知失败/IMAP 抖动）：保留待重试，绝不因此放弃，
			// 否则一次基础设施抖动就会永久丢信。基础设施恢复后自然成功。
			if _, ok := failed[key]; !ok {
				failed[key] = 0 // 入队但不推进放弃计数
			}
			p.log(fmt.Sprintf("%s✗ uid=%d 暂时性故障，保留重试(不计入放弃): %s", tgt.label, uid, err.Error()))
		}
		*tgt.lastUID = advanceWatermark([]uint32{uid}, *tgt.lastUID)
		*tgt.failed = failed
		*tgt.uidv = uidv
		if err := p.st.Save(); err != nil {
			return err
		}
		time.Sleep(p.pace)
	}

	// 水位线只推进到本轮 todo 里实际覆盖的最大 uid（retry 的旧 uid < last 不影响）。
	*tgt.failed = failed
	*tgt.uidv = uidv
	p.log(fmt.Sprintf("%s本轮完成。水位 last_uid=%d，待重试 %d 封。", tgt.label, *tgt.lastUID, len(failed)))
	return nil
}

func pruneDeliveryMap(delivered state.DeliveryMap, allSet map[uint32]bool) {
	for k := range delivered {
		v, err := strconv.ParseUint(k, 10, 32)
		if err != nil || !allSet[uint32(v)] {
			delete(delivered, k)
		}
	}
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
