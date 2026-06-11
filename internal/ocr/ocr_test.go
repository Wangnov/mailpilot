package ocr

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestAllowedOCRResultURL(t *testing.T) {
	if !allowedOCRResultURL("https://paddleocr.example.com/jobs", "https://storage.example.com/result.jsonl") {
		t.Fatal("https public result should be allowed")
	}
	if allowedOCRResultURL("http://paddleocr.example.com/jobs", "http://100.64.0.1/result.jsonl") {
		t.Fatal("CGNAT result should be rejected")
	}
	if allowedOCRResultURL("https://paddleocr.example.com/jobs", "http://169.254.169.254/result.jsonl") {
		t.Fatal("link-local result should be rejected")
	}
	if allowedOCRResultURL("https://paddleocr.example.com/jobs", "http://localhost/result.jsonl") {
		t.Fatal("localhost result should be rejected")
	}
	if !allowedOCRResultURL("http://127.0.0.1:1234/jobs", "http://127.0.0.1:1234/result.jsonl") {
		t.Fatal("same-host local test server should be allowed")
	}
	if safeResultIP(net.ParseIP("100.64.0.1"), false) {
		t.Fatal("CGNAT IP should be rejected")
	}
	if !safeResultIP(net.ParseIP("8.8.8.8"), false) {
		t.Fatal("public IP should be allowed")
	}
	if !safeResultIP(net.ParseIP("127.0.0.1"), true) {
		t.Fatal("same-host local test IP should be allowed")
	}
}

func TestFetchOCRTextDoesNotFollowRedirect(t *testing.T) {
	private := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"result":{"layoutParsingResults":[{"markdown":{"text":"secret"}}]}}`))
	}))
	defer private.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, private.URL+"/result.jsonl", http.StatusFound)
	}))
	defer srv.Close()

	p := &paddleEngine{
		cfg:    config.OCR{JobURL: srv.URL + "/jobs"},
		log:    func(string) {},
		client: srv.Client(),
		sleep:  func(time.Duration) {},
	}
	if got := p.fetchOCRText(t.Context(), srv.URL+"/result.jsonl"); got != "" {
		t.Fatalf("redirected result should not be read, got %q", got)
	}
}

func TestFetchOCRTextRejectsObfuscatedLocalAddress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"result":{"layoutParsingResults":[{"markdown":{"text":"secret"}}]}}`))
	}))
	defer srv.Close()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatal(err)
	}

	p := &paddleEngine{
		cfg:    config.OCR{JobURL: "http://paddleocr.example.com/jobs"},
		log:    func(string) {},
		client: srv.Client(),
		sleep:  func(time.Duration) {},
	}
	if got := p.fetchOCRText(t.Context(), "http://2130706433:"+port+"/result.jsonl"); got != "" {
		t.Fatalf("obfuscated local result should not be read, got %q", got)
	}
}
