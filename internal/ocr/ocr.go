// Package ocr 调用 PaddleOCR 在线 API（异步 job + 轮询）识别图片文字。
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

	"github.com/Wangnov/mailpilot-go/internal/config"
)

// Images 逐张识别并合并文字；未启用/无 token/失败返回空串。
func Images(images [][]byte, cfg config.OCR, log func(string)) string {
	if !cfg.Enabled || cfg.Token == "" || len(images) == 0 {
		return ""
	}
	var out []string
	for i, img := range images {
		if t := ocrOne(img, cfg); t != "" {
			out = append(out, t)
		} else {
			log(fmt.Sprintf("OCR 第%d张无结果", i))
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func ocrOne(img []byte, cfg config.OCR) string {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("model", cfg.Model)
	opt, _ := json.Marshal(map[string]bool{
		"useDocOrientationClassify": false,
		"useDocUnwarping":           false,
		"useChartRecognition":       false,
	})
	_ = w.WriteField("optionalPayload", string(opt))
	fw, _ := w.CreateFormFile("file", "image.png")
	_, _ = fw.Write(img)
	_ = w.Close()

	client := &http.Client{Timeout: 60 * time.Second}
	req, _ := http.NewRequest("POST", cfg.JobURL, &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "bearer "+cfg.Token)
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	jobID := dataString(resp.Body, "jobId")
	resp.Body.Close()
	if jobID == "" {
		return ""
	}

	for i := 0; i < 30; i++ {
		r, err := client.Get(cfg.JobURL + "/" + jobID)
		if err != nil {
			return ""
		}
		var jr map[string]any
		_ = json.NewDecoder(r.Body).Decode(&jr)
		r.Body.Close()
		data, _ := jr["data"].(map[string]any)
		state, _ := data["state"].(string)
		switch state {
		case "pending", "running":
			time.Sleep(4 * time.Second)
		case "done":
			ru, _ := data["resultUrl"].(map[string]any)
			jsonURL, _ := ru["jsonUrl"].(string)
			return fetchOCRText(jsonURL)
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

func fetchOCRText(url string) string {
	if url == "" {
		return ""
	}
	resp, err := http.Get(url)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
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
