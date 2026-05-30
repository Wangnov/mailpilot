// Package config 加载 YAML 配置，支持 ${ENV} 环境变量展开 + 默认值。
package config

import (
	"os"
	"reflect"
	"regexp"

	"gopkg.in/yaml.v3"
)

type IMAP struct {
	Host      string `yaml:"host"`
	User      string `yaml:"user"`
	Password  string `yaml:"password"`
	Mailbox   string `yaml:"mailbox"`
	ForceIPv4 bool   `yaml:"force_ipv4"`
}

type Provider struct {
	Type    string `yaml:"type"` // codex | openai | ollama
	Model   string `yaml:"model"`
	BaseURL string `yaml:"base_url"`
	APIKey  string `yaml:"api_key"`
}

type Analyze struct {
	Providers []Provider `yaml:"providers"`
	Timeout   int        `yaml:"timeout"`
	Language  string     `yaml:"language"` // 通知正文(摘要/要点/建议)的输出语言，如 中文 / English / 日本語；auto=随邮件语言
}

type OCR struct {
	Enabled bool   `yaml:"enabled"`
	Type    string `yaml:"type"`
	Token   string `yaml:"token"`
	Model   string `yaml:"model"`
	JobURL  string `yaml:"job_url"`
	MinBody int    `yaml:"min_body"`
}

type Notifier struct {
	Type     string  `yaml:"type"` // bark | telegram | ntfy | webhook
	Key      string  `yaml:"key"`
	Server   string  `yaml:"server"`
	Icon     *string `yaml:"icon"` // Bark 推送图标 URL；不设=内置 logo，设为 "" 可关闭，设 URL 可自定义
	BotToken string  `yaml:"bot_token"`
	ChatID   string  `yaml:"chat_id"`
	Topic    string  `yaml:"topic"`
	URL      string  `yaml:"url"`
}

type Pipeline struct {
	BaselineOnFirstRun bool     `yaml:"baseline_on_first_run"`
	HistorySearch      bool     `yaml:"history_search"`
	MaxPerRun          int      `yaml:"max_per_run"`
	MaxRetry           int      `yaml:"max_retry"`
	IdleTimeout        int      `yaml:"idle_timeout"`
	MaxBodyChars       int      `yaml:"max_body_chars"`
	StatePath          string   `yaml:"state_path"`
	SkipCategories     []string `yaml:"skip_categories"` // 命中这些分类的邮件只分析、不推送(如 [垃圾, 营销推广])；空=全部推送
}

type Config struct {
	IMAP     IMAP       `yaml:"imap"`
	Analyze  Analyze    `yaml:"analyze"`
	OCR      OCR        `yaml:"ocr"`
	Notify   []Notifier `yaml:"notify"`
	Pipeline Pipeline   `yaml:"pipeline"`
}

var envRe = regexp.MustCompile(`\$\{([^}]+)\}`)

// Load 读取 YAML，解析后再在【字符串值】上展开 ${ENV}，最后应用默认值。
// 在解析后展开（而非对原始文本替换）可避免密钥里的 YAML 特殊字符(: # " 换行)破坏解析。
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	expandEnv(reflect.ValueOf(&c).Elem())
	c.applyDefaults()
	return &c, nil
}

// expandEnv 递归地把所有字符串字段里的 ${ENV} 替换为环境变量值。
func expandEnv(v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			expandEnv(v.Elem())
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if f := v.Field(i); f.CanSet() {
				expandEnv(f)
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			expandEnv(v.Index(i))
		}
	case reflect.String:
		v.SetString(envRe.ReplaceAllStringFunc(v.String(), func(m string) string {
			return os.Getenv(m[2 : len(m)-1])
		}))
	}
}

func (c *Config) applyDefaults() {
	d := func(p *string, v string) {
		if *p == "" {
			*p = v
		}
	}
	di := func(p *int, v int) {
		if *p == 0 {
			*p = v
		}
	}
	d(&c.IMAP.Host, "imap.gmail.com")
	d(&c.IMAP.Mailbox, "INBOX")
	di(&c.Analyze.Timeout, 300)
	d(&c.Analyze.Language, "中文")
	d(&c.OCR.Model, "PaddleOCR-VL-1.6")
	d(&c.OCR.JobURL, "https://paddleocr.aistudio-app.com/api/v2/ocr/jobs")
	di(&c.OCR.MinBody, 30)
	di(&c.Pipeline.MaxPerRun, 20)
	di(&c.Pipeline.MaxRetry, 3)
	di(&c.Pipeline.IdleTimeout, 300)
	di(&c.Pipeline.MaxBodyChars, 12000)
	d(&c.Pipeline.StatePath, "state.json")
}
