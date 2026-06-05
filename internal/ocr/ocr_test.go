package ocr

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wangnov/mailpilot/internal/config"
)

func TestBuildEngines(t *testing.T) {
	e, err := Build(config.OCR{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if e.Name() != "noop" {
		t.Fatalf("disabled OCR engine=%s, want noop", e.Name())
	}

	_, err = Build(config.OCR{Enabled: true, Type: "unknown"}, nil)
	if err == nil || !strings.Contains(err.Error(), "未知 OCR 引擎类型") {
		t.Fatalf("unknown OCR type err=%v", err)
	}
}

func TestPaddleEngineImages(t *testing.T) {
	var polls int
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/jobs":
			if r.Method != http.MethodPost {
				t.Fatalf("job method=%s", r.Method)
			}
			if got := r.Header.Get("Authorization"); got != "bearer tok" {
				t.Fatalf("authorization=%q", got)
			}
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatalf("multipart: %v", err)
			}
			if got := r.FormValue("model"); got != "paddle-test" {
				t.Fatalf("model=%q", got)
			}
			_, _ = w.Write([]byte(`{"data":{"jobId":"job-1"}}`))
		case "/jobs/job-1":
			polls++
			if polls == 1 {
				_, _ = w.Write([]byte(`{"data":{"state":"running"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":{"state":"done","resultUrl":{"jsonUrl":"` + srv.URL + `/result.jsonl"}}}`))
		case "/result.jsonl":
			_, _ = w.Write([]byte(`{"result":{"layoutParsingResults":[{"markdown":{"text":"验证码 294817"}}]}}` + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := &paddleEngine{
		cfg: config.OCR{
			Enabled: true,
			Type:    "paddle",
			Token:   "tok",
			Model:   "paddle-test",
			JobURL:  srv.URL + "/jobs",
		},
		log:    func(string) {},
		client: srv.Client(),
		sleep:  func(time.Duration) {},
	}
	got := p.Images([][]byte{[]byte("fake-image")})
	if got != "验证码 294817" {
		t.Fatalf("OCR text=%q", got)
	}
	if polls != 2 {
		t.Fatalf("polls=%d, want 2", polls)
	}
}
