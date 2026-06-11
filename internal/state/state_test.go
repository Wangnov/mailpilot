package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBaselineRoundtrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.json")
	s, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.BaselineDone {
		t.Fatal("new state should not be baseline-done")
	}
	s.SetBaseline(1, 100)
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	s2, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if !s2.BaselineDone || s2.LastUID != 100 || s2.UIDValidity != 1 {
		t.Errorf("roundtrip mismatch: %+v", s2)
	}
	s2.Failed["106"] = 1
	_ = s2.Save()
	s3, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if s3.Failed["106"] != 1 {
		t.Error("failed map not persisted")
	}
}

func TestSaveCreatesParentDirectory(t *testing.T) {
	p := filepath.Join(t.TempDir(), "runtime", "state.json")
	s, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	s.SetBaseline(2, 200)
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastUID != 200 || got.UIDValidity != 2 {
		t.Fatalf("nested state mismatch: %+v", got)
	}
}

func TestLoadRejectsCorruptState(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.json")
	if err := os.WriteFile(p, []byte(`{"last_uid":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("corrupt state should fail closed")
	}
}
