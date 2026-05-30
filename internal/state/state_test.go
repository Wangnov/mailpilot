package state

import (
	"path/filepath"
	"testing"
)

func TestBaselineRoundtrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.json")
	s := Load(p)
	if s.BaselineDone {
		t.Fatal("new state should not be baseline-done")
	}
	s.SetBaseline(1, 100)
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	s2 := Load(p)
	if !s2.BaselineDone || s2.LastUID != 100 || s2.UIDValidity != 1 {
		t.Errorf("roundtrip mismatch: %+v", s2)
	}
	s2.Failed["106"] = 1
	_ = s2.Save()
	if Load(p).Failed["106"] != 1 {
		t.Error("failed map not persisted")
	}
}
