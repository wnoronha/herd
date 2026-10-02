package tailcat

import (
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
	"herd/internal/identity"
)

const (
	// PacketMagic identifies a Tailcat packet
	PacketMagic = byte(0x54) // 'T'
	NonceSize   = chacha20poly1305.NonceSize
)

var defaultClusterKey = func() identity.Key {
	var k identity.Key
	h := sha256.Sum256([]byte("herd-mesh-cluster-discovery-v1"))
	copy(k[:], h[:])
	return k
}()

// ClusterDiscoveryKey returns the default cluster discovery key.
func ClusterDiscoveryKey() identity.Key {
	return defaultClusterKey
}

// DeriveSharedSecret computes the 32-byte Curve25519 Diffie-Hellman shared secret between a private key and remote public key.
func DeriveSharedSecret(privKey identity.Key, remotePub identity.Key) (identity.Key, error) {
	var shared identity.Key
	sharedBytes, err := curve25519.X25519(privKey[:], remotePub[:])
	if err != nil {
		return shared, fmt.Errorf("curve25519 ecdh failed: %w", err)
	}
	copy(shared[:], sharedBytes)
	return shared, nil
}

// EncryptPacketWithKey encrypts the plaintext payload using a derived shared secret and sender identity.
func EncryptPacketWithKey(id *identity.Identity, sharedKey identity.Key, plaintext []byte) ([]byte, error) {
	aead, err := chacha20poly1305.New(sharedKey[:])
	if err != nil {
		return nil, fmt.Errorf("failed to init aead cipher: %w", err)
	}

	nonce := make([]byte, NonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Format: [Magic:1][SenderPubKey:32][Nonce:12][Ciphertext+Tag]
	headerLen := 1 + identity.KeySize + NonceSize
	out := make([]byte, headerLen, headerLen+len(plaintext)+aead.Overhead())
	out[0] = PacketMagic
	copy(out[1:1+identity.KeySize], id.PublicKey[:])
	copy(out[1+identity.KeySize:headerLen], nonce)

	ciphertext := aead.Seal(out, nonce, plaintext, out[:headerLen])
	return ciphertext, nil
}

// DecryptPacketWithPrivKey authenticates and decrypts an incoming Tailcat packet using the receiver's private key.
func DecryptPacketWithPrivKey(privKey identity.Key, packet []byte) ([]byte, identity.Key, error) {
	headerLen := 1 + identity.KeySize + NonceSize
	if len(packet) < headerLen+chacha20poly1305.Overhead {
		return nil, identity.Key{}, fmt.Errorf("packet too short (%d bytes)", len(packet))
	}

	if packet[0] != PacketMagic {
		return nil, identity.Key{}, fmt.Errorf("invalid packet magic: 0x%x", packet[0])
	}

	var senderPub identity.Key
	copy(senderPub[:], packet[1:1+identity.KeySize])
	nonce := packet[1+identity.KeySize : headerLen]
	ciphertext := packet[headerLen:]

	// 1. Derive shared secret from sender's public key and our private key (ECDH)
	sharedKey, err := DeriveSharedSecret(privKey, senderPub)
	if err == nil {
		if aead, err := chacha20poly1305.New(sharedKey[:]); err == nil {
			if plaintext, err := aead.Open(nil, nonce, ciphertext, packet[:headerLen]); err == nil {
				return plaintext, senderPub, nil
			}
		}
	}

	// 2. Fallback to cluster discovery key
	clusterKey := ClusterDiscoveryKey()
	if aead, err := chacha20poly1305.New(clusterKey[:]); err == nil {
		if plaintext, err := aead.Open(nil, nonce, ciphertext, packet[:headerLen]); err == nil {
			return plaintext, senderPub, nil
		}
	}

	return nil, senderPub, fmt.Errorf("packet decryption/authentication failed")
}

// EncryptPacket encrypts the plaintext payload using sender identity and cluster discovery key.
func EncryptPacket(id *identity.Identity, plaintext []byte) ([]byte, error) {
	return EncryptPacketWithKey(id, ClusterDiscoveryKey(), plaintext)
}

// DecryptPacket authenticates and decrypts an incoming Tailcat packet with PSK.
func DecryptPacket(psk identity.Key, packet []byte) ([]byte, identity.Key, error) {
	headerLen := 1 + identity.KeySize + NonceSize
	if len(packet) < headerLen+chacha20poly1305.Overhead {
		return nil, identity.Key{}, fmt.Errorf("packet too short (%d bytes)", len(packet))
	}

	if packet[0] != PacketMagic {
		return nil, identity.Key{}, fmt.Errorf("invalid packet magic: 0x%x", packet[0])
	}

	var senderPub identity.Key
	copy(senderPub[:], packet[1:1+identity.KeySize])
	nonce := packet[1+identity.KeySize : headerLen]
	ciphertext := packet[headerLen:]

	aead, err := chacha20poly1305.New(psk[:])
	if err != nil {
		return nil, identity.Key{}, fmt.Errorf("failed to create aead cipher: %w", err)
	}

	plaintext, err := aead.Open(nil, nonce, ciphertext, packet[:headerLen])
	if err != nil {
		return nil, identity.Key{}, fmt.Errorf("packet decryption/authentication failed: %w", err)
	}

	return plaintext, senderPub, nil
}

// NewStreamCipher creates an AEAD cipher using the given key for stream framing.
func NewStreamCipher(key identity.Key) (cipher.AEAD, error) {
	return chacha20poly1305.New(key[:])
}
