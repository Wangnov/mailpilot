package analyze

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Wangnov/mailpilot/internal/config"
	"github.com/Wangnov/mailpilot/internal/imap"
)

// openaiProvider 调 OpenAI / 任意兼容端点。withHistory 时跑 function-calling
// agent loop（多轮 tool_call→执行→回灌），最后一次强制 json_schema 结构化输出。
type openaiProvider struct {
	cfg      config.Provider
	timeout  int
	language string
}

func (p *openaiProvider) Name() string        { return "openai:" + p.cfg.Model }
func (p *openaiProvider) SupportsTools() bool { return true }

const maxToolRounds = 4

func (p *openaiProvider) Analyze(m *imap.Mail, withHistory bool, toolCmd string) (*Analysis, error) {
	messages := []map[string]any{
		{"role": "system", "content": systemPromptFor(p.language)},
		{"role": "user", "content": buildStdin(m)},
	}
	if withHistory {
		messages = append(messages, map[string]any{
			"role":    "system",
			"content": "这封邮件可能是某讨论串/issue/PR 的后续。请先调用 mail_search 工具检索相关历史邮件（例如 action=search query=\"subject:关键词\"，或 action=thread query=该邮件uid），读懂来龙去脉后再分析。",
		})
		messages = p.agentLoop(messages)
	}
	return p.finalStructured(messages)
}

// agentLoop 让模型自主多轮调用 mail_search 检索历史，把对话上下文累积进 messages。
func (p *openaiProvider) agentLoop(messages []map[string]any) []map[string]any {
	tools := []map[string]any{mailSearchTool()}
	for round := 0; round < maxToolRounds; round++ {
		raw, err := p.post(map[string]any{"model": p.cfg.Model, "messages": messages, "tools": tools})
		if err != nil {
			break
		}
		msg, calls := parseChoice(raw)
		if msg != nil {
			messages = append(messages, msg)
		}
		if len(calls) == 0 {
			break // 模型不再检索，进入最终结构化
		}
		for _, tc := range calls {
			fmt.Fprintf(os.Stderr, "[openai agent] 第%d轮检索: %s\n", round+1, tc.argsJSON)
			messages = append(messages, map[string]any{
				"role": "tool", "tool_call_id": tc.id, "content": execToolSearch(tc.argsJSON),
			})
		}
	}
	return messages
}

// finalStructured 不带 tools、强制 json_schema，得到最终结构化结果。
func (p *openaiProvider) finalStructured(messages []map[string]any) (*Analysis, error) {
	raw, err := p.post(map[string]any{
		"model":    p.cfg.Model,
		"messages": messages,
		"response_format": map[string]any{
			"type":        "json_schema",
			"json_schema": map[string]any{"name": "mail_analysis", "schema": OutputSchema, "strict": true},
		},
	})
	if err != nil {
		return nil, transientErr(err) // 调用层故障：网络/限流/非 200，暂时性
	}
	a, err := parseAnalysis([]byte(choiceContent(raw)))
	if err != nil {
		return nil, droppableErr(err) // 模型已响应但产物无法解析
	}
	return a, nil
}

func (p *openaiProvider) post(body map[string]any) ([]byte, error) {
	base := p.cfg.BaseURL
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	buf, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", strings.TrimRight(base, "/")+"/chat/completions", bytes.NewReader(buf))
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
	return raw, nil
}

type toolCall struct {
	id       string
	argsJSON string
}

// parseChoice 返回 assistant 消息(原样, 供回灌)与其中的 tool_calls。
func parseChoice(raw []byte) (map[string]any, []toolCall) {
	var r struct {
		Choices []struct {
			Message json.RawMessage `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(raw, &r) != nil || len(r.Choices) == 0 {
		return nil, nil
	}
	var msg map[string]any
	_ = json.Unmarshal(r.Choices[0].Message, &msg)
	var calls []toolCall
	if tcs, ok := msg["tool_calls"].([]any); ok {
		for _, tc := range tcs {
			mp, _ := tc.(map[string]any)
			id, _ := mp["id"].(string)
			fn, _ := mp["function"].(map[string]any)
			args, _ := fn["arguments"].(string)
			calls = append(calls, toolCall{id: id, argsJSON: args})
		}
	}
	return msg, calls
}

func choiceContent(raw []byte) string {
	var r struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	_ = json.Unmarshal(raw, &r)
	if len(r.Choices) > 0 {
		return r.Choices[0].Message.Content
	}
	return ""
}

func mailSearchTool() map[string]any {
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        "mail_search",
			"description": "只读检索历史邮件，辅助理解当前邮件的来龙去脉。",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{"type": "string", "enum": []string{"search", "thread", "get"}, "description": "search=按Gmail语法搜索; thread=按uid取同主题; get=按uid取正文"},
					"query":  map[string]any{"type": "string", "description": "search 时为搜索语法(如 subject:xxx / from:yyy)；thread/get 时为邮件 uid"},
				},
				"required": []string{"action", "query"},
			},
		},
	}
}

// execToolSearch 调用自身 tool-search 子命令复用同一检索实现（不挂框架）。
func execToolSearch(argsJSON string) string {
	var a struct {
		Action string `json:"action"`
		Query  string `json:"query"`
	}
	if json.Unmarshal([]byte(argsJSON), &a) != nil || a.Action == "" {
		return "(工具参数解析失败)"
	}
	self, err := os.Executable()
	if err != nil || self == "" {
		self = "mailpilot"
	}
	out, _ := exec.Command(self, "tool-search", a.Action, a.Query).CombinedOutput()
	s := string(out)
	if len(s) > 4000 {
		s = s[:4000]
	}
	return s
}
