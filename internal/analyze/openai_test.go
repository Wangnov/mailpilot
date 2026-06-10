package analyze

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wangnov/mailpilot/internal/config"
)

func TestParseChoiceToolCalls(t *testing.T) {
	raw := []byte(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call-1","type":"function","function":{"name":"mail_search","arguments":"{\"action\":\"search\",\"query\":\"subject:test\"}"}}]}}]}`)
	msg, calls := parseChoice(raw)
	if msg == nil || msg["role"] != "assistant" {
		t.Fatalf("message=%v", msg)
	}
	if len(calls) != 1 || calls[0].id != "call-1" || calls[0].argsJSON == "" {
		t.Fatalf("calls=%+v", calls)
	}
}

func TestChoiceContent(t *testing.T) {
	raw := []byte(`{"choices":[{"message":{"content":"hello"}}]}`)
	if got := choiceContent(raw); got != "hello" {
		t.Fatalf("content=%q", got)
	}
	if got := choiceContent([]byte(`{bad json`)); got != "" {
		t.Fatalf("bad json content=%q", got)
	}
}

func TestOpenAIFinalStructured(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer key" {
			t.Fatalf("authorization=%q", auth)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		content := `{"category":"工作","urgency":"中","summary":"需要处理","needs_reply":true,"key_points":["a"],"suggested_action":"回复","verification_code":"","action_url":"https://example.com/ticket/1"}`
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": content}}},
		})
	}))
	defer srv.Close()

	p := &openaiProvider{
		cfg:      config.Provider{Type: "openai", Model: "m", APIKey: "key", BaseURL: srv.URL},
		timeout:  5,
		language: "中文",
	}
	a, err := p.finalStructured([]map[string]any{{"role": "user", "content": "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if a.Category != "工作" || !a.NeedsReply {
		t.Fatalf("analysis=%+v", a)
	}
	if got["model"] != "m" || got["response_format"] == nil {
		t.Fatalf("request body=%v", got)
	}
}

func TestOpenAIFinalStructuredErrorKinds(t *testing.T) {
	t.Run("api failure is transient", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`rate limited`))
		}))
		defer srv.Close()

		p := &openaiProvider{cfg: config.Provider{Model: "m", BaseURL: srv.URL}, timeout: 5}
		_, err := p.finalStructured(nil)
		if err == nil || IsDroppable(err) {
			t.Fatalf("err=%v droppable=%v", err, IsDroppable(err))
		}
	})

	t.Run("bad model JSON is droppable", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{"message": map[string]any{"content": "not json"}}},
			})
		}))
		defer srv.Close()

		p := &openaiProvider{cfg: config.Provider{Model: "m", BaseURL: srv.URL}, timeout: 5}
		_, err := p.finalStructured(nil)
		if err == nil || !IsDroppable(err) {
			t.Fatalf("err=%v droppable=%v", err, IsDroppable(err))
		}
	})
}

func TestOpenAIPostRejectsBadBaseURL(t *testing.T) {
	p := &openaiProvider{cfg: config.Provider{Model: "m", BaseURL: "://bad-url"}, timeout: 5}
	if _, err := p.post(map[string]any{}); err == nil {
		t.Fatal("bad base_url should fail")
	}
}
