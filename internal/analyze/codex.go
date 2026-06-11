package analyze

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/Wangnov/mailpilot/internal/config"
	"github.com/Wangnov/mailpilot/internal/imap"
)

// codexProvider 用 ChatGPT 订阅(codex CLI)分析，支持 agentic 历史检索。
// 约束：只用临时参数(-c/-m/-s)与环境变量注入，绝不修改 ~/.codex 配置；
// 所有临时产物都落在【项目内】workRoot 下并随用随清，绝不污染系统 /tmp、家目录或 ~/.codex。
type codexProvider struct {
	cfg      config.Provider
	timeout  int
	workRoot string // 项目内的 codex 临时工作根目录（由 BuildProvider 注入）
	language string // 通知自由文本的输出语言
}

func (p *codexProvider) Name() string        { return "codex:" + p.cfg.Model }
func (p *codexProvider) SupportsTools() bool { return false }

func (p *codexProvider) Analyze(m *imap.Mail, withHistory bool, toolCmd string) (*Analysis, error) {
	prompt := buildPrompt(withHistory, toolCmd, m.UID, p.language)

	// 所有临时产物都放在项目内的 workRoot 下并在结束后清理；绝不写系统 /tmp / 家目录 / ~/.codex。
	// workRoot 必须由调用方注入（BuildProvider 取项目目录下的 .mailpilot-work）——
	// 宁可报错降级到下一个 provider，也不退回系统临时目录污染用户系统。
	base := p.workRoot
	if base == "" {
		return nil, transientErr(fmt.Errorf("codex workRoot 未配置（拒绝退回系统临时目录）"))
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return nil, transientErr(fmt.Errorf("创建 codex 工作目录失败: %w", err))
	}

	// schema 放在沙箱【外】（codex 只读它，不可被沙箱内命令改写）。
	f, err := os.CreateTemp(base, "schema-*.json")
	if err != nil {
		return nil, transientErr(err)
	}
	defer os.Remove(f.Name())
	_ = json.NewEncoder(f).Encode(OutputSchema)
	f.Close()

	// sandbox 是 codex 的空 CWD；read-only sandbox 和最小环境避免触碰邮件配置与密钥。
	sandbox, err := os.MkdirTemp(base, "codex-cwd-")
	if err != nil {
		return nil, transientErr(err)
	}
	defer os.RemoveAll(sandbox)

	bin := os.Getenv("CODEX_BIN")
	if bin == "" {
		bin = "codex"
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(p.timeout)*time.Second)
	defer cancel()
	// --ephemeral：不把 session 落盘到 ~/.codex，避免污染用户家目录；
	// -c/-m/-s 都是本次调用的临时覆盖，不写回 ~/.codex/config.toml。
	cmd := exec.CommandContext(ctx, bin, "exec", "-m", p.cfg.Model,
		"--ephemeral",
		"--sandbox", "read-only",
		"-c", "sandbox_workspace_write.network_access=false",
		"--skip-git-repo-check", "-C", sandbox,
		"--output-schema", f.Name(), prompt)
	cmd.Env = minimalCodexEnv()
	cmd.Stdin = strings.NewReader(buildStdin(m))
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, transientErr(fmt.Errorf("codex 执行失败: %s", tail(errb.String(), 300)))
	}
	a, err := parseAnalysis(out.Bytes())
	if err != nil {
		return nil, droppableErr(err) // codex 回了内容但解析失败：归为可丢弃
	}
	return a, nil
}

func minimalCodexEnv() []string {
	keep := []string{
		"HOME", "PATH", "USER", "LOGNAME", "SHELL", "LANG", "LC_ALL", "LC_CTYPE", "TERM", "CODEX_HOME",
		"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY",
		"http_proxy", "https_proxy", "all_proxy", "no_proxy",
		"SSL_CERT_FILE", "SSL_CERT_DIR", "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE", "NODE_EXTRA_CA_CERTS",
	}
	if runtime.GOOS == "windows" {
		keep = append(keep, "APPDATA", "LOCALAPPDATA", "USERPROFILE")
	}
	out := make([]string, 0, len(keep))
	for _, k := range keep {
		if v, ok := os.LookupEnv(k); ok {
			out = append(out, k+"="+v)
		}
	}
	return out
}
