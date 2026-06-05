// Package state 维护去重水位线 last_uid + 失败重试队列，原子写盘。
package state

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type State struct {
	path         string
	UIDValidity  uint32         `json:"uidvalidity"`
	LastUID      uint32         `json:"last_uid"`
	Failed       map[string]int `json:"failed"`
	BaselineDone bool           `json:"baseline_done"`
	// 垃圾箱兜底扫描的独立水位线（uid 与 INBOX 不同名空间，必须分开）。
	SpamUIDValidity  uint32         `json:"spam_uidvalidity,omitempty"`
	SpamLastUID      uint32         `json:"spam_last_uid,omitempty"`
	SpamFailed       map[string]int `json:"spam_failed,omitempty"`
	SpamBaselineDone bool           `json:"spam_baseline_done,omitempty"`
}

func Load(path string) *State {
	s := &State{Failed: map[string]int{}}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, s)
	}
	if s.Failed == nil {
		s.Failed = map[string]int{}
	}
	if s.SpamFailed == nil {
		s.SpamFailed = map[string]int{}
	}
	s.path = path
	return s
}

func (s *State) Save() error {
	data, _ := json.MarshalIndent(s, "", "  ")
	if dir := filepath.Dir(s.path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *State) SetBaseline(uidValidity, lastUID uint32) {
	s.UIDValidity = uidValidity
	s.LastUID = lastUID
	s.Failed = map[string]int{}
	s.BaselineDone = true
}
