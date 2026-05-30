package analyze

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wangnov/mailpilot/internal/config"
	"github.com/Wangnov/mailpilot/internal/imap"
)

// openaiProvider 调 OpenAI / 任意兼容端点，结构化输出(json_schema strict)。
type openaiProvider struct {
	cfg     config.Provider
	timeout int
}

func (p *openaiProvider) Name() string       { return "openai:" + p.cfg.Model }
func (p *openaiProvider) SupportsTools() bool { return false }

func (p *openaiProvider) Analyze(m *imap.Mail, withHistory bool, toolCmd string) (*Analysis, error) {
	base := p.cfg.BaseURL
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	base = strings.TrimRight(base, "/")
	body := map[string]any{
		"model": p.cfg.Model,
		"messages": []map[string]string{
			{"role": "system", "content": SystemPrompt},
			{"role": "user", "content": buildStdin(m)},
		},
		"response_format": map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name": "mail_analysis", "schema": OutputSchema, "strict": true,
			},
		},
	}
	buf, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", base+"/chat/completions", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	if p.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	}
	client := &http.Client{Timeout: time.Duration(p.timeout) * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("OpenAI API %d: %s", resp.StatusCode, tail(string(raw), 200))
	}
	var r struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &r); err != nil || len(r.Choices) == 0 {
		return nil, fmt.Errorf("OpenAI 响应解析失败: %s", tail(string(raw), 200))
	}
	return parseAnalysis([]byte(r.Choices[0].Message.Content))
}
