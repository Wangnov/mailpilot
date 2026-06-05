// Package ocr 抽象图片文字识别引擎，默认实现是 PaddleOCR 在线 API。
package ocr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
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
			client: &http.Client{Timeout: 60 * time.Second},
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

	req, err := http.NewRequest("POST", p.cfg.JobURL, &buf)
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
		r, err := p.client.Get(strings.TrimRight(p.cfg.JobURL, "/") + "/" + jobID)
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
			return p.fetchOCRText(jsonURL)
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

func (p *paddleEngine) fetchOCRText(url string) string {
	if url == "" {
		return ""
	}
	resp, err := p.client.Get(url)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return ""
	}
	body, _ := io.ReadAll(resp.Body)
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
