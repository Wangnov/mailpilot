package analyze

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/Wangnov/mailpilot/internal/config"
	"github.com/Wangnov/mailpilot/internal/imap"
)

// 用本机 codex CLI 端到端验证 codexProvider（含 #5：在空临时目录沙箱里运行）。
// 默认跳过，设 CODEX_E2E=1 运行；可用 CODEX_TEST_MODEL 覆盖模型。
func TestCodexProviderE2E(t *testing.T) {
	if os.Getenv("CODEX_E2E") != "1" {
		t.Skip("设 CODEX_E2E=1 运行 codex 端到端测试")
	}
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("codex CLI 未安装")
	}
	model := os.Getenv("CODEX_TEST_MODEL")
	if model == "" {
		model = "gpt-5.3-codex-spark"
	}
	// workRoot 指向隔离的临时目录，验证“产物只落在指定项目目录内”这条路径。
	p := &codexProvider{cfg: config.Provider{Type: "codex", Model: model}, timeout: 240, workRoot: t.TempDir()}
	m := &imap.Mail{
		UID:     1,
		From:    "Google <no-reply@accounts.google.com>",
		Subject: "安全提醒：检测到新设备登录",
		Date:    "Sat, 30 May 2026 10:00:00 +0800",
		Body:    "我们检测到您的 Google 账号在一台新设备上登录。如果这是您本人操作，无需理会。验证码：294817。",
	}
	a, err := p.Analyze(m, false, "")
	if err != nil {
		t.Fatalf("codex Analyze 失败: %v", err)
	}
	if a.Category == "" || a.Urgency == "" {
		t.Fatalf("结果关键字段为空: %+v", a)
	}
	t.Logf("codex 结果: category=%s urgency=%s needs_reply=%v summary=%q points=%v",
		a.Category, a.Urgency, a.NeedsReply, a.Summary, a.KeyPoints)
}

func TestMinimalCodexEnvDropsMailSecrets(t *testing.T) {
	t.Setenv("IMAP_PASSWORD", "secret")
	t.Setenv("OPENAI_API_KEY", "secret")
	t.Setenv("BARK_KEY", "secret")
	t.Setenv("MAILPILOT_CONFIG", "/tmp/config.yaml")
	t.Setenv("PATH", "/bin")
	t.Setenv("HTTPS_PROXY", "http://proxy.example:8080")
	t.Setenv("NO_PROXY", "localhost,127.0.0.1")
	t.Setenv("SSL_CERT_FILE", "/etc/ssl/corp.pem")
	t.Setenv("REQUESTS_CA_BUNDLE", "/etc/ssl/requests.pem")

	env := strings.Join(minimalCodexEnv(), "\n")
	for _, key := range []string{"IMAP_PASSWORD=", "OPENAI_API_KEY=", "BARK_KEY=", "MAILPILOT_CONFIG="} {
		if strings.Contains(env, key) {
			t.Fatalf("secret env %s should not be passed: %s", key, env)
		}
	}
	if !strings.Contains(env, "PATH=/bin") {
		t.Fatalf("PATH should be preserved: %s", env)
	}
	for _, want := range []string{
		"HTTPS_PROXY=http://proxy.example:8080",
		"NO_PROXY=localhost,127.0.0.1",
		"SSL_CERT_FILE=/etc/ssl/corp.pem",
		"REQUESTS_CA_BUNDLE=/etc/ssl/requests.pem",
	} {
		if !strings.Contains(env, want) {
			t.Fatalf("network/cert env %q should be preserved: %s", want, env)
		}
	}
}
