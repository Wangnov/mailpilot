// Package state 维护去重水位线 last_uid + 失败重试队列，原子写盘。
package state

import (
	"encoding/json"
	"os"
)

type State struct {
	path         string
	UIDValidity  uint32         `json:"uidvalidity"`
	LastUID      uint32         `json:"last_uid"`
	Failed       map[string]int `json:"failed"`
	BaselineDone bool           `json:"baseline_done"`
}

func Load(path string) *State {
	s := &State{Failed: map[string]int{}}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, s)
	}
	if s.Failed == nil {
		s.Failed = map[string]int{}
	}
	s.path = path
	return s
}

func (s *State) Save() error {
	data, _ := json.MarshalIndent(s, "", "  ")
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
