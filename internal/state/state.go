// Package state 维护去重水位线 last_uid + 失败重试队列，原子写盘。
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
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
	Delivered        DeliveryMap    `json:"delivered,omitempty"`
	SpamDelivered    DeliveryMap    `json:"spam_delivered,omitempty"`
}

type DeliveryMap map[string]map[string]bool

func Load(path string) (*State, error) {
	s := &State{Failed: map[string]int{}}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, s); err != nil {
			return nil, fmt.Errorf("解析状态文件失败: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("读取状态文件失败: %w", err)
	}
	if s.Failed == nil {
		s.Failed = map[string]int{}
	}
	if s.SpamFailed == nil {
		s.SpamFailed = map[string]int{}
	}
	if s.Delivered == nil {
		s.Delivered = DeliveryMap{}
	}
	if s.SpamDelivered == nil {
		s.SpamDelivered = DeliveryMap{}
	}
	s.path = path
	return s, nil
}

func (s *State) Save() error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(s.path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return err
	}
	if dirFile, err := os.Open(dir); err == nil {
		_ = dirFile.Sync()
		_ = dirFile.Close()
	}
	return nil
}

func (s *State) SetBaseline(uidValidity, lastUID uint32) {
	s.UIDValidity = uidValidity
	s.LastUID = lastUID
	s.Failed = map[string]int{}
	s.BaselineDone = true
}

type FileLock struct {
	f *os.File
}

func AcquireLock(statePath string) (*FileLock, error) {
	lockPath := statePath + ".lock"
	dir := filepath.Dir(lockPath)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &FileLock{f: f}, nil
}

func (l *FileLock) Close() error {
	if l == nil || l.f == nil {
		return nil
	}
	errUnlock := syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	errClose := l.f.Close()
	if errUnlock != nil {
		return errUnlock
	}
	return errClose
}
