package analyze

import (
	"context"
	"time"

	"github.com/Wangnov/mailpilot/internal/config"
	"github.com/Wangnov/mailpilot/internal/imap"
	"google.golang.org/genai"
)

// geminiProvider 用官方 Gemini Go SDK（原生格式）分析；结构化输出走 responseSchema。
// 单轮（无 agentic 历史检索，类似 ollama）；要 agentic 历史用 codex / openai。
type geminiProvider struct {
	cfg      config.Provider
	timeout  int
	language string
	client   *genai.Client
}

func newGeminiProvider(cfg config.Provider, timeout int, language string) (*geminiProvider, error) {
	// API Key 走 Gemini API 后端（非 Vertex）。base_url 默认官方端点。
	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:  cfg.APIKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, err
	}
	return &geminiProvider{cfg: cfg, timeout: timeout, language: language, client: client}, nil
}

func (p *geminiProvider) Name() string        { return "gemini:" + p.cfg.Model }
func (p *geminiProvider) SupportsTools() bool { return false }

// geminiSchema 把 OutputSchema 映射成 Gemini 原生 Schema（枚举/必填等约束等价）。
func geminiSchema() *genai.Schema {
	return &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"category":          {Type: genai.TypeString, Enum: []string{"工作", "财务", "账单", "营销推广", "通知", "个人", "验证码", "垃圾", "其他"}},
			"urgency":           {Type: genai.TypeString, Enum: []string{"高", "中", "低"}},
			"summary":           {Type: genai.TypeString},
			"needs_reply":       {Type: genai.TypeBoolean},
			"key_points":        {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString}},
			"suggested_action":  {Type: genai.TypeString},
			"verification_code": {Type: genai.TypeString},
			"action_url":        {Type: genai.TypeString},
		},
		Required: []string{"category", "urgency", "summary", "needs_reply", "key_points", "suggested_action", "verification_code", "action_url"},
	}
}

func (p *geminiProvider) Analyze(m *imap.Mail, _ bool, _ string) (*Analysis, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(p.timeout)*time.Second)
	defer cancel()
	cfg := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{Parts: []*genai.Part{{Text: systemPromptFor(p.language)}}},
		ResponseMIMEType:  "application/json",
		ResponseSchema:    geminiSchema(),
	}
	resp, err := p.client.Models.GenerateContent(ctx, p.cfg.Model, genai.Text(buildStdin(m)), cfg)
	if err != nil {
		return nil, transientErr(err) // 网络/限流/鉴权等调用层故障：暂时性
	}
	a, err := parseAnalysis([]byte(resp.Text()))
	if err != nil {
		return nil, droppableErr(err) // 模型已响应但产物无法解析
	}
	return a, nil
}
