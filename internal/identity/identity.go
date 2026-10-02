package identity

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/curve25519"
)

const (
	KeySize = 32
)

// Key represents a 32-byte cryptographic key.
type Key [KeySize]byte

// String returns the Base64 encoding of the key.
func (k Key) String() string {
	return base64.StdEncoding.EncodeToString(k[:])
}

// MarshalText implements encoding.TextMarshaler for JSON serialization.
func (k Key) MarshalText() ([]byte, error) {
	return []byte(k.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler for JSON deserialization.
func (k *Key) UnmarshalText(text []byte) error {
	decoded, err := base64.StdEncoding.DecodeString(string(text))
	if err != nil {
		return fmt.Errorf("invalid base64 key: %w", err)
	}
	if len(decoded) != KeySize {
		return fmt.Errorf("invalid key length: got %d, expected %d", len(decoded), KeySize)
	}
	copy(k[:], decoded)
	return nil
}

// GeneratePrivateKey generates a new Curve25519 / WireGuard private key.
func GeneratePrivateKey() (Key, error) {
	var priv Key
	if _, err := io.ReadFull(rand.Reader, priv[:]); err != nil {
		return priv, fmt.Errorf("failed to generate private key: %w", err)
	}
	// Clamp private key according to Curve25519 specification
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64
	return priv, nil
}

// Public calculates the Curve25519 public key corresponding to this private key.
func (k Key) Public() (Key, error) {
	var pub Key
	curve25519.ScalarBaseMult((*[32]byte)(&pub), (*[32]byte)(&k))
	return pub, nil
}

// GeneratePSK generates a 256-bit (32-byte) pre-shared key.
func GeneratePSK() (Key, error) {
	var psk Key
	if _, err := io.ReadFull(rand.Reader, psk[:]); err != nil {
		return psk, fmt.Errorf("failed to generate PSK: %w", err)
	}
	return psk, nil
}

// Identity holds the node identity and cryptographic keys.
type Identity struct {
	NodeName     string    `json:"node_name"`
	PrivateKey   Key       `json:"private_key"`
	PublicKey    Key       `json:"public_key"`
	PreSharedKey Key       `json:"preshared_key"`
	CreatedAt    time.Time `json:"created_at"`
}

// NewIdentity creates a new node identity with freshly generated keys.
func NewIdentity(nodeName string) (*Identity, error) {
	priv, err := GeneratePrivateKey()
	if err != nil {
		return nil, err
	}
	pub, err := priv.Public()
	if err != nil {
		return nil, err
	}
	psk, err := GeneratePSK()
	if err != nil {
		return nil, err
	}

	return &Identity{
		NodeName:     nodeName,
		PrivateKey:   priv,
		PublicKey:    pub,
		PreSharedKey: psk,
		CreatedAt:    time.Now().UTC(),
	}, nil
}

// TailcatAddr returns the address string or node ID representation derived from the public key.
func (id *Identity) TailcatAddr() string {
	return id.PublicKey.String()
}

// Save persists the identity to the given file path with 0600 permissions.
func (id *Identity) Save(filePath string) error {
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create directory for identity: %w", err)
	}

	data, err := json.MarshalIndent(id, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal identity: %w", err)
	}

	// Write with 0600 permissions
	tmpFile := filePath + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0600); err != nil {
		return fmt.Errorf("failed to write identity tmp file: %w", err)
	}

	// Ensure permissions are strictly 0600
	if err := os.Chmod(tmpFile, 0600); err != nil {
		_ = os.Remove(tmpFile)
		return fmt.Errorf("failed to chmod identity file: %w", err)
	}

	if err := os.Rename(tmpFile, filePath); err != nil {
		_ = os.Remove(tmpFile)
		return fmt.Errorf("failed to atomically save identity file: %w", err)
	}

	return nil
}

// Load loads an identity from the given file path.
func Load(filePath string) (*Identity, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read identity file: %w", err)
	}

	var id Identity
	if err := json.Unmarshal(data, &id); err != nil {
		return nil, fmt.Errorf("failed to parse identity file: %w", err)
	}

	// Recalculate public key to verify integrity
	expectedPub, err := id.PrivateKey.Public()
	if err != nil {
		return nil, fmt.Errorf("corrupt private key: %w", err)
	}
	if expectedPub != id.PublicKey {
		return nil, fmt.Errorf("public key mismatch in stored identity")
	}

	return &id, nil
}

// LoadOrGenerate loads the identity from $dataDir/identity.json or generates and persists a new one.
func LoadOrGenerate(dataDir string, nodeName string) (*Identity, error) {
	identityPath := filepath.Join(dataDir, "identity.json")
	if _, err := os.Stat(identityPath); err == nil {
		id, err := Load(identityPath)
		if err == nil {
			return id, nil
		}
		// If load fails because of corruption, return error
		return nil, fmt.Errorf("existing identity file invalid at %s: %w", identityPath, err)
	}

	id, err := NewIdentity(nodeName)
	if err != nil {
		return nil, fmt.Errorf("failed to generate identity: %w", err)
	}

	if err := id.Save(identityPath); err != nil {
		return nil, fmt.Errorf("failed to save generated identity: %w", err)
	}

	return id, nil
}
