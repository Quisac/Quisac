package store

import (
	"os"
	"path/filepath"
	"testing"
)

func tmpStore(t *testing.T) (*DatadogStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "datadog-secrets.json")
	ds, err := NewDatadogStore(path)
	if err != nil {
		t.Fatalf("NewDatadogStore: %v", err)
	}
	return ds, path
}

func TestProvision_NewInstallation(t *testing.T) {
	ds, _ := tmpStore(t)
	secret, err := ds.Provision("install-1")
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if secret == "" {
		t.Fatal("expected non-empty secret")
	}
}

func TestProvision_Idempotent(t *testing.T) {
	ds, _ := tmpStore(t)
	s1, _ := ds.Provision("install-1")
	s2, _ := ds.Provision("install-1")
	if s1 != s2 {
		t.Fatalf("expected same secret on re-provision, got %q and %q", s1, s2)
	}
}

func TestRotate_ChangesSecret(t *testing.T) {
	ds, _ := tmpStore(t)
	original, _ := ds.Provision("install-1")
	rotated, err := ds.Rotate("install-1")
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if original == rotated {
		t.Fatal("expected secret to change after rotation")
	}
}

func TestSecret_ReturnsCorrectValue(t *testing.T) {
	ds, _ := tmpStore(t)
	provisioned, _ := ds.Provision("install-1")
	got, ok := ds.Secret("install-1")
	if !ok {
		t.Fatal("expected Secret to return true")
	}
	if got != provisioned {
		t.Fatalf("expected %q got %q", provisioned, got)
	}
}

func TestSecret_MissingInstallation(t *testing.T) {
	ds, _ := tmpStore(t)
	_, ok := ds.Secret("does-not-exist")
	if ok {
		t.Fatal("expected ok=false for unknown installation")
	}
}

func TestInstallationBySecret_Found(t *testing.T) {
	ds, _ := tmpStore(t)
	secret, _ := ds.Provision("install-1")
	id, ok := ds.InstallationBySecret(secret)
	if !ok {
		t.Fatal("expected to find installation by secret")
	}
	if id != "install-1" {
		t.Fatalf("expected install-1 got %q", id)
	}
}

func TestInstallationBySecret_NotFound(t *testing.T) {
	ds, _ := tmpStore(t)
	_, ok := ds.InstallationBySecret("not-a-real-secret")
	if ok {
		t.Fatal("expected ok=false for unknown secret")
	}
}

func TestPersistence_ReloadsFromDisk(t *testing.T) {
	ds, path := tmpStore(t)
	secret, _ := ds.Provision("install-1")
	ds2, err := NewDatadogStore(path)
	if err != nil {
		t.Fatalf("NewDatadogStore reload: %v", err)
	}
	got, ok := ds2.Secret("install-1")
	if !ok {
		t.Fatal("expected secret to persist across reload")
	}
	if got != secret {
		t.Fatalf("expected %q got %q", secret, got)
	}
}

func TestPersistence_EmptyFileOnFirstLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "datadog-secrets.json")
	ds, err := NewDatadogStore(path)
	if err != nil {
		t.Fatalf("NewDatadogStore on missing file: %v", err)
	}
	_, ok := ds.Secret("any")
	if ok {
		t.Fatal("expected empty store")
	}
}

func TestMultipleInstallations(t *testing.T) {
	ds, _ := tmpStore(t)
	s1, _ := ds.Provision("install-1")
	s2, _ := ds.Provision("install-2")
	if s1 == s2 {
		t.Fatal("expected different secrets for different installations")
	}
	id1, ok1 := ds.InstallationBySecret(s1)
	id2, ok2 := ds.InstallationBySecret(s2)
	if !ok1 || id1 != "install-1" {
		t.Fatalf("wrong lookup for install-1: ok=%v id=%q", ok1, id1)
	}
	if !ok2 || id2 != "install-2" {
		t.Fatalf("wrong lookup for install-2: ok=%v id=%q", ok2, id2)
	}
}

func TestFilePermissions(t *testing.T) {
	ds, path := tmpStore(t)
	ds.Provision("install-1")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("expected 0600 permissions, got %v", info.Mode().Perm())
	}
}
