package analyze

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wangnov/mailpilot-go/internal/config"
	"github.com/Wangnov/mailpilot-go/internal/imap"
)

// ollamaProvider 本地模型，隐私优先零成本。用 format=schema 约束输出。
type ollamaProvider struct {
	cfg     config.Provider
	timeout int
}

func (p *ollamaProvider) Name() string       { return "ollama:" + p.cfg.Model }
func (p *ollamaProvider) SupportsTools() bool { return false }

func (p *ollamaProvider) Analyze(m *imap.Mail, withHistory bool, toolCmd string) (*Analysis, error) {
	base := p.cfg.BaseURL
	if base == "" {
		base = "http://localhost:11434"
	}
	base = strings.TrimRight(base, "/")
	body := map[string]any{
		"model": p.cfg.Model,
		"messages": []map[string]string{
			{"role": "system", "content": SystemPrompt + "\n只输出符合要求的 JSON。"},
			{"role": "user", "content": buildStdin(m)},
		},
		"stream": false,
		"format": OutputSchema,
	}
	buf, _ := json.Marshal(body)
	client := &http.Client{Timeout: time.Duration(p.timeout) * time.Second}
	resp, err := client.Post(base+"/api/chat", "application/json", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Ollama %d: %s", resp.StatusCode, tail(string(raw), 200))
	}
	var r struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("Ollama 响应解析失败: %s", tail(string(raw), 200))
	}
	return parseAnalysis([]byte(r.Message.Content))
}
