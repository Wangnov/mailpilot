package analyze

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Wangnov/mailpilot/internal/config"
	"github.com/Wangnov/mailpilot/internal/imap"
)

// codexProvider 用 ChatGPT 订阅(codex CLI)分析，支持 agentic 历史检索。
type codexProvider struct {
	cfg     config.Provider
	timeout int
	workdir string
}

func (p *codexProvider) Name() string       { return "codex:" + p.cfg.Model }
func (p *codexProvider) SupportsTools() bool { return true }

func (p *codexProvider) Analyze(m *imap.Mail, withHistory bool, toolCmd string) (*Analysis, error) {
	prompt := buildPrompt(withHistory, toolCmd, m.UID)

	f, err := os.CreateTemp("", "mp-schema-*.json")
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	_ = json.NewEncoder(f).Encode(OutputSchema)
	f.Close()

	bin := os.Getenv("CODEX_BIN")
	if bin == "" {
		bin = "codex"
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(p.timeout)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "exec", "-m", p.cfg.Model,
		"--sandbox", "workspace-write",
		"-c", "sandbox_workspace_write.network_access=true",
		"--skip-git-repo-check", "-C", p.workdir,
		"--output-schema", f.Name(), prompt)
	cmd.Stdin = strings.NewReader(buildStdin(m))
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("codex 执行失败: %s", tail(errb.String(), 300))
	}
	return parseAnalysis(out.Bytes())
}
