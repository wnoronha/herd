package identity

import (
	"os"
	"path/filepath"
	"testing"
)

func TestKeyGeneration(t *testing.T) {
	priv, err := GeneratePrivateKey()
	if err != nil {
		t.Fatalf("GeneratePrivateKey() error: %v", err)
	}

	pub, err := priv.Public()
	if err != nil {
		t.Fatalf("priv.Public() error: %v", err)
	}

	if pub == priv {
		t.Errorf("public key should not equal private key")
	}

	pub2, err := priv.Public()
	if err != nil {
		t.Fatalf("priv.Public() second call error: %v", err)
	}
	if pub != pub2 {
		t.Errorf("public key derivation must be deterministic")
	}

	psk1, err := GeneratePSK()
	if err != nil {
		t.Fatalf("GeneratePSK() error: %v", err)
	}
	psk2, err := GeneratePSK()
	if err != nil {
		t.Fatalf("GeneratePSK() second call error: %v", err)
	}
	if psk1 == psk2 {
		t.Errorf("expected randomly generated PSKs to differ")
	}
	if len(psk1) != KeySize {
		t.Errorf("PSK length %d, expected %d", len(psk1), KeySize)
	}
}

func TestIdentityPersistenceAndPermissions(t *testing.T) {
	tmpDir := t.TempDir()
	idFile := filepath.Join(tmpDir, "identity.json")

	id, err := NewIdentity("test-node")
	if err != nil {
		t.Fatalf("NewIdentity() error: %v", err)
	}

	if err := id.Save(idFile); err != nil {
		t.Fatalf("id.Save() error: %v", err)
	}

	// Verify file permissions 0600
	info, err := os.Stat(idFile)
	if err != nil {
		t.Fatalf("os.Stat() error: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("expected file permissions 0600, got %o", perm)
	}

	// Load identity back
	loaded, err := Load(idFile)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if loaded.NodeName != id.NodeName {
		t.Errorf("expected node name %s, got %s", id.NodeName, loaded.NodeName)
	}
	if loaded.PrivateKey != id.PrivateKey {
		t.Errorf("expected private key %v, got %v", id.PrivateKey, loaded.PrivateKey)
	}
	if loaded.PublicKey != id.PublicKey {
		t.Errorf("expected public key %v, got %v", id.PublicKey, loaded.PublicKey)
	}
	if loaded.PreSharedKey != id.PreSharedKey {
		t.Errorf("expected PSK %v, got %v", id.PreSharedKey, loaded.PreSharedKey)
	}
	if loaded.TailcatAddr() != id.TailcatAddr() {
		t.Errorf("expected TailcatAddr %s, got %s", id.TailcatAddr(), loaded.TailcatAddr())
	}
}

func TestLoadOrGenerate(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Initial generation
	id1, err := LoadOrGenerate(tmpDir, "node-one")
	if err != nil {
		t.Fatalf("LoadOrGenerate() run 1 failed: %v", err)
	}

	// 2. Second load should produce identical identity and TailcatAddr
	id2, err := LoadOrGenerate(tmpDir, "node-one")
	if err != nil {
		t.Fatalf("LoadOrGenerate() run 2 failed: %v", err)
	}

	if id1.TailcatAddr() != id2.TailcatAddr() {
		t.Errorf("TailcatAddr changed across restarts: %s != %s", id1.TailcatAddr(), id2.TailcatAddr())
	}
	if id1.PrivateKey != id2.PrivateKey {
		t.Errorf("PrivateKey changed across restarts")
	}
}
