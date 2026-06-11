// Package ocr 抽象图片文字识别引擎，默认实现是 PaddleOCR 在线 API。
package ocr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/Wangnov/mailpilot/internal/config"
)

// Engine 是 pipeline 依赖的 OCR 引擎接口。新增供应商时只需实现这里。
type Engine interface {
	Name() string
	Images(images [][]byte) string
}

type noopEngine struct{}

func (noopEngine) Name() string             { return "noop" }
func (noopEngine) Images(_ [][]byte) string { return "" }

type paddleEngine struct {
	cfg    config.OCR
	log    func(string)
	client *http.Client
	sleep  func(time.Duration)
}

// Build 根据配置构造 OCR 引擎。type 为空时由 config 默认成 paddle；未启用时返回 noop。
func Build(cfg config.OCR, log func(string)) (Engine, error) {
	if !cfg.Enabled {
		return noopEngine{}, nil
	}
	if log == nil {
		log = func(string) {}
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Type)) {
	case "", "paddle", "paddleocr":
		return &paddleEngine{
			cfg: cfg, log: log,
			client: &http.Client{Timeout: 30 * time.Second},
			sleep:  time.Sleep,
		}, nil
	default:
		return nil, fmt.Errorf("未知 OCR 引擎类型: %s", cfg.Type)
	}
}

// Images 是兼容旧调用的便捷函数；新代码优先持有 Engine，避免 pipeline 绑定具体供应商。
func Images(images [][]byte, cfg config.OCR, log func(string)) string {
	e, err := Build(cfg, log)
	if err != nil {
		return ""
	}
	return e.Images(images)
}

func (p *paddleEngine) Name() string { return "paddle" }

// Images 逐张识别并合并文字；未启用/无 token/失败返回空串。
func (p *paddleEngine) Images(images [][]byte) string {
	if p.cfg.Token == "" || len(images) == 0 {
		return ""
	}
	var out []string
	for i, img := range images {
		if t := p.ocrOne(img); t != "" {
			out = append(out, t)
		} else {
			p.log(fmt.Sprintf("OCR 第%d张无结果", i))
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func (p *paddleEngine) ocrOne(img []byte) string {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("model", p.cfg.Model)
	opt, _ := json.Marshal(map[string]bool{
		"useDocOrientationClassify": false,
		"useDocUnwarping":           false,
		"useChartRecognition":       false,
	})
	_ = w.WriteField("optionalPayload", string(opt))
	fw, _ := w.CreateFormFile("file", "image.png")
	_, _ = fw.Write(img)
	_ = w.Close()

	req, err := http.NewRequestWithContext(ctx, "POST", p.cfg.JobURL, &buf)
	if err != nil {
		return ""
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "bearer "+p.cfg.Token)
	resp, err := p.client.Do(req)
	if err != nil {
		return ""
	}
	if resp.StatusCode >= 300 {
		resp.Body.Close()
		return ""
	}
	jobID := dataString(resp.Body, "jobId")
	resp.Body.Close()
	if jobID == "" {
		return ""
	}

	for i := 0; i < 30; i++ {
		pollReq, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(p.cfg.JobURL, "/")+"/"+jobID, nil)
		if err != nil {
			return ""
		}
		r, err := p.client.Do(pollReq)
		if err != nil {
			return ""
		}
		if r.StatusCode >= 300 {
			r.Body.Close()
			return ""
		}
		var jr map[string]any
		_ = json.NewDecoder(r.Body).Decode(&jr)
		r.Body.Close()
		data, _ := jr["data"].(map[string]any)
		state, _ := data["state"].(string)
		switch state {
		case "pending", "running":
			p.sleep(4 * time.Second)
		case "done":
			ru, _ := data["resultUrl"].(map[string]any)
			jsonURL, _ := ru["jsonUrl"].(string)
			return p.fetchOCRText(ctx, jsonURL)
		default:
			return ""
		}
	}
	return ""
}

func dataString(body io.Reader, key string) string {
	var jr map[string]any
	if json.NewDecoder(body).Decode(&jr) != nil {
		return ""
	}
	data, _ := jr["data"].(map[string]any)
	s, _ := data[key].(string)
	return s
}

func (p *paddleEngine) fetchOCRText(ctx context.Context, rawURL string) string {
	if !allowedOCRResultURL(p.cfg.JobURL, rawURL) {
		return ""
	}
	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return ""
	}
	resp, err := p.doNoRedirect(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return ""
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	var sb strings.Builder
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		if line == "" {
			continue
		}
		var obj map[string]any
		if json.Unmarshal([]byte(line), &obj) != nil {
			continue
		}
		result, _ := obj["result"].(map[string]any)
		lps, _ := result["layoutParsingResults"].([]any)
		for _, lp := range lps {
			mp, _ := lp.(map[string]any)
			md, _ := mp["markdown"].(map[string]any)
			if t, ok := md["text"].(string); ok {
				sb.WriteString(t)
				sb.WriteByte('\n')
			}
		}
	}
	return sb.String()
}

func (p *paddleEngine) doNoRedirect(req *http.Request) (*http.Response, error) {
	client := http.Client{}
	if p.client != nil {
		client = *p.client
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	client.Transport = safeResultTransport(client.Transport, p.cfg.JobURL, req.URL)
	return client.Do(req)
}

func safeResultTransport(base http.RoundTripper, jobURL string, target *url.URL) http.RoundTripper {
	tr, ok := base.(*http.Transport)
	if ok {
		tr = tr.Clone()
	} else {
		tr = http.DefaultTransport.(*http.Transport).Clone()
	}
	tr.Proxy = nil
	tr.DialContext = safeResultDialContext(jobURL, target)
	return tr
}

func safeResultDialContext(jobURL string, target *url.URL) func(context.Context, string, string) (net.Conn, error) {
	allowLocal := sameHTTPHost(jobURL, target)
	dialer := &net.Dialer{}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		if len(addrs) == 0 {
			return nil, fmt.Errorf("ocr result host has no addresses")
		}
		for _, addr := range addrs {
			if !safeResultIP(addr.IP, allowLocal) {
				return nil, fmt.Errorf("ocr result host resolves to private address")
			}
		}
		var lastErr error
		for _, addr := range addrs {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(addr.IP.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
}

func sameHTTPHost(jobURL string, target *url.URL) bool {
	job, err := url.Parse(jobURL)
	return err == nil && job.Scheme == "http" && target != nil && target.Scheme == "http" && strings.EqualFold(job.Host, target.Host)
}

func safeResultIP(ip net.IP, allowLocal bool) bool {
	if ip == nil {
		return false
	}
	if allowLocal {
		return true
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	addr = addr.Unmap()
	if !addr.IsValid() || !addr.IsGlobalUnicast() {
		return false
	}
	for _, prefix := range blockedResultPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

var blockedResultPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

func allowedOCRResultURL(jobURL, rawURL string) bool {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || !u.IsAbs() || u.Host == "" {
		return false
	}
	job, _ := url.Parse(jobURL)
	if u.Scheme != "https" && !(job != nil && job.Scheme == "http" && u.Scheme == "http") {
		return false
	}
	if job != nil && job.Scheme == "http" && u.Scheme == "http" && strings.EqualFold(u.Host, job.Host) {
		return true
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return safeResultIP(ip, false)
	}
	return true
}
