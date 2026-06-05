package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEnvExpandAndDefaults(t *testing.T) {
	t.Setenv("MP_PW", "secret")
	t.Setenv("MP_BK", "barkkey")
	p := filepath.Join(t.TempDir(), "c.yaml")
	yaml := "imap:\n  user: a@b.com\n  password: ${MP_PW}\n" +
		"analyze:\n  providers:\n    - type: openai\n      model: gpt-5.4-mini\n" +
		"notify:\n  - type: bark\n    key: ${MP_BK}\n"
	if err := os.WriteFile(p, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.IMAP.Password != "secret" {
		t.Errorf("password=%q, want secret", c.IMAP.Password)
	}
	if c.Notify[0].Key != "barkkey" {
		t.Errorf("key=%q, want barkkey", c.Notify[0].Key)
	}
	if c.IMAP.Host != "imap.gmail.com" {
		t.Errorf("default host not applied: %q", c.IMAP.Host)
	}
	if c.Pipeline.MaxPerRun != 20 {
		t.Errorf("default max_per_run=%d, want 20", c.Pipeline.MaxPerRun)
	}
	if c.OCR.Type != "paddle" {
		t.Errorf("default ocr.type=%q, want paddle", c.OCR.Type)
	}
}

// 回归：密钥含 YAML 特殊字符(: # " 空格)时，解析后展开不应被破坏。
func TestLoadEnvSpecialChars(t *testing.T) {
	weird := `p:a#s s"w'd`
	t.Setenv("MP_WEIRD", weird)
	p := filepath.Join(t.TempDir(), "c.yaml")
	yaml := "imap:\n  user: a@b.com\n  password: ${MP_WEIRD}\n" +
		"analyze:\n  providers:\n    - type: openai\n      api_key: ${MP_WEIRD}\n"
	if err := os.WriteFile(p, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatalf("含特殊字符的密钥不应导致解析失败: %v", err)
	}
	if c.IMAP.Password != weird {
		t.Errorf("password=%q, want %q", c.IMAP.Password, weird)
	}
	if c.Analyze.Providers[0].APIKey != weird {
		t.Errorf("api_key=%q, want %q", c.Analyze.Providers[0].APIKey, weird)
	}
}
