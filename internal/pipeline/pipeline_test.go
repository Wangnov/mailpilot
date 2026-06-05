package pipeline

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Wangnov/mailpilot/internal/config"
	"github.com/Wangnov/mailpilot/internal/imap"
)

func setOf(uids []uint32) map[uint32]bool {
	s := make(map[uint32]bool, len(uids))
	for _, u := range uids {
		s[u] = true
	}
	return s
}

// 回归：backlog 超过 maxPerRun 时，跨两轮处理必须【零丢失】（旧 bug 会静默丢最旧的）。
func TestPlanTodoNoLossAcrossRuns(t *testing.T) {
	var all []uint32
	for u := uint32(101); u <= 130; u++ { // 30 封新邮件，超过 maxPerRun=20
		all = append(all, u)
	}
	allSet := setOf(all)
	const maxPerRun, maxRetry = 20, 3

	processed := map[uint32]bool{}
	last := uint32(100)
	for round := 0; round < 5; round++ {
		todo, _, _, total := planTodo(all, allSet, last, map[string]int{}, maxPerRun, maxRetry)
		if total == 0 {
			break
		}
		// 必须最旧优先且升序
		if !sort.SliceIsSorted(todo, func(i, j int) bool { return todo[i] < todo[j] }) {
			t.Fatalf("round %d: todo 未升序: %v", round, todo)
		}
		if len(todo) > maxPerRun {
			t.Fatalf("round %d: todo 超上限 %d: %d", round, maxPerRun, len(todo))
		}
		for _, u := range todo {
			if processed[u] {
				t.Errorf("round %d: uid=%d 被重复处理", round, u)
			}
			processed[u] = true
		}
		last = advanceWatermark(todo, last)
	}
	for u := uint32(101); u <= 130; u++ {
		if !processed[u] {
			t.Errorf("uid=%d 被静默丢弃（未处理）", u)
		}
	}
	if last != 130 {
		t.Errorf("最终水位=%d, want 130", last)
	}
}

// retry 的旧 uid 进入 todo，但不能把水位线拉低或错误推高。
func TestPlanTodoRetryAndWatermark(t *testing.T) {
	all := []uint32{50, 200, 201}
	allSet := setOf(all)
	failed := map[string]int{"50": 1, "999": 1} // 999 已不在邮箱 → 应被忽略
	todo, newCount, retryCount, total := planTodo(all, allSet, 200, failed, 20, 3)
	if newCount != 1 || retryCount != 1 || total != 2 {
		t.Fatalf("new=%d retry=%d total=%d, want 1/1/2", newCount, retryCount, total)
	}
	if len(todo) != 2 || todo[0] != 50 || todo[1] != 201 {
		t.Fatalf("todo=%v, want [50 201]", todo)
	}
	if w := advanceWatermark(todo, 200); w != 201 {
		t.Errorf("watermark=%d, want 201 (retry 的 50 不应拉低)", w)
	}
}

// 达到 maxRetry 的失败 uid 不再被选入重试。
func TestPlanTodoMaxRetryExhausted(t *testing.T) {
	all := []uint32{10}
	_, _, retryCount, total := planTodo(all, setOf(all), 100, map[string]int{"10": 3}, 20, 3)
	if retryCount != 0 || total != 0 {
		t.Errorf("retry=%d total=%d, want 0/0 (已达上限不再重试)", retryCount, total)
	}
}

func TestSkipCategory(t *testing.T) {
	skip := []string{"垃圾", "营销推广"}
	if !skipCategory("垃圾", skip) || !skipCategory("营销推广", skip) {
		t.Error("命中名单的分类应被跳过")
	}
	if skipCategory("工作", skip) {
		t.Error("未命中的分类不应被跳过")
	}
	if skipCategory("垃圾", nil) {
		t.Error("空名单不应跳过任何分类")
	}
}

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

func TestNewRequiresProviderAndNotifier(t *testing.T) {
	_, err := New(nil, filepath.Join(t.TempDir(), "config.yaml"), nil)
	if err == nil || !strings.Contains(err.Error(), "配置为空") {
		t.Fatalf("nil config err=%v", err)
	}

	_, err = New(&config.Config{Notify: []config.Notifier{{Type: "bark", Key: "k"}}}, filepath.Join(t.TempDir(), "config.yaml"), nil)
	if err == nil || !strings.Contains(err.Error(), "analyze provider") {
		t.Fatalf("missing provider err=%v", err)
	}

	_, err = New(&config.Config{
		Analyze: config.Analyze{Providers: []config.Provider{{Type: "openai", Model: "m"}}},
	}, filepath.Join(t.TempDir(), "config.yaml"), nil)
	if err == nil || !strings.Contains(err.Error(), "notify 渠道") {
		t.Fatalf("missing notifier err=%v", err)
	}
}

func TestNewResolvesStatePathRelativeToConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Analyze: config.Analyze{Providers: []config.Provider{{Type: "openai", Model: "m"}}},
		Notify:  []config.Notifier{{Type: "bark", Key: "k"}},
		OCR:     config.OCR{Enabled: false},
		Pipeline: config.Pipeline{
			StatePath: "runtime/state.json",
		},
	}
	p, err := New(cfg, filepath.Join(dir, "config.yaml"), func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "runtime", "state.json")
	if p.cfg.Pipeline.StatePath != want {
		t.Fatalf("state path=%q, want %q", p.cfg.Pipeline.StatePath, want)
	}
}

func TestNewRejectsUnknownOCREngine(t *testing.T) {
	_, err := New(&config.Config{
		Analyze: config.Analyze{Providers: []config.Provider{{Type: "openai", Model: "m"}}},
		Notify:  []config.Notifier{{Type: "bark", Key: "k"}},
		OCR:     config.OCR{Enabled: true, Type: "made-up"},
	}, filepath.Join(t.TempDir(), "config.yaml"), nil)
	if err == nil || !strings.Contains(err.Error(), "未知 OCR 引擎类型") {
		t.Fatalf("unknown OCR engine err=%v", err)
	}
}
