package tailcat

import (
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"herd/internal/identity"
)

// secureConn wraps a net.Conn with ChaCha20-Poly1305 framed encryption.
type secureConn struct {
	net.Conn
	aead      cipher.AEAD
	readBuf   []byte
	readMu    sync.Mutex
	writeMu   sync.Mutex
	remotePub identity.Key
}

func newSecureConn(raw net.Conn, psk identity.Key, remotePub identity.Key) (*secureConn, error) {
	aead, err := NewStreamCipher(psk)
	if err != nil {
		return nil, err
	}
	return &secureConn{
		Conn:      raw,
		aead:      aead,
		remotePub: remotePub,
	}, nil
}

func (c *secureConn) Read(b []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()

	for len(c.readBuf) == 0 {
		var length uint32
		if err := binary.Read(c.Conn, binary.BigEndian, &length); err != nil {
			return 0, err
		}

		if length > 10*1024*1024 { // 10MB sanity frame limit
			return 0, fmt.Errorf("stream frame exceeds maximum size: %d", length)
		}

		frame := make([]byte, length)
		if _, err := io.ReadFull(c.Conn, frame); err != nil {
			return 0, err
		}

		if len(frame) < NonceSize+c.aead.Overhead() {
			return 0, fmt.Errorf("corrupt stream frame (length %d)", len(frame))
		}

		nonce := frame[:NonceSize]
		ciphertext := frame[NonceSize:]
		plaintext, err := c.aead.Open(nil, nonce, ciphertext, nil)
		if err != nil {
			return 0, fmt.Errorf("stream frame decryption failed: %w", err)
		}

		c.readBuf = plaintext
	}

	n := copy(b, c.readBuf)
	c.readBuf = c.readBuf[n:]
	return n, nil
}

func (c *secureConn) Write(b []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	nonce := make([]byte, NonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return 0, fmt.Errorf("failed to generate stream nonce: %w", err)
	}

	ciphertext := c.aead.Seal(nil, nonce, b, nil)
	frame := make([]byte, 4+NonceSize+len(ciphertext))
	binary.BigEndian.PutUint32(frame[:4], uint32(NonceSize+len(ciphertext)))
	copy(frame[4:4+NonceSize], nonce)
	copy(frame[4+NonceSize:], ciphertext)

	_, err := c.Conn.Write(frame)
	if err != nil {
		return 0, err
	}
	return len(b), nil
}

const (
	// StreamTypeGossip is for memberlist gossip push/pull sync.
	StreamTypeGossip = byte(0x01)
	// StreamTypeExec is for remote command execution.
	StreamTypeExec = byte(0x02)
	// StreamTypeForward is for dynamic port proxying.
	StreamTypeForward = byte(0x03)
	// StreamTypeFile is for file transfer/sync.
	StreamTypeFile = byte(0x04)
	// StreamTypeAgent is for direct encrypted agent-to-agent dialog & tool streams.
	StreamTypeAgent = byte(0x05)
	// StreamTypePacket is for reliable framed memberlist gossip packet datagrams.
	StreamTypePacket = byte(0x06)
)

// performClientHandshake exchanges public keys and transmits requested stream type.
func performClientHandshake(conn net.Conn, id *identity.Identity, streamType byte) (identity.Key, error) {
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	payload := make([]byte, identity.KeySize+1)
	copy(payload[:identity.KeySize], id.PublicKey[:])
	payload[identity.KeySize] = streamType

	if _, err := conn.Write(payload); err != nil {
		return identity.Key{}, fmt.Errorf("handshake write failed: %w", err)
	}

	var remotePub identity.Key
	if _, err := io.ReadFull(conn, remotePub[:]); err != nil {
		return identity.Key{}, fmt.Errorf("handshake read failed: %w", err)
	}

	_ = conn.SetDeadline(time.Time{})
	return remotePub, nil
}

// performServerHandshake exchanges public keys and reads client's requested stream type.
func performServerHandshake(conn net.Conn, id *identity.Identity) (identity.Key, byte, error) {
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	if _, err := conn.Write(id.PublicKey[:]); err != nil {
		return identity.Key{}, 0, fmt.Errorf("handshake write failed: %w", err)
	}

	clientPayload := make([]byte, identity.KeySize+1)
	if _, err := io.ReadFull(conn, clientPayload); err != nil {
		return identity.Key{}, 0, fmt.Errorf("handshake read failed: %w", err)
	}

	var remotePub identity.Key
	copy(remotePub[:], clientPayload[:identity.KeySize])
	streamType := clientPayload[identity.KeySize]

	_ = conn.SetDeadline(time.Time{})
	return remotePub, streamType, nil
}

// PerformHandshake is a compatibility wrapper defaulting to StreamTypeGossip.
func PerformHandshake(conn net.Conn, id *identity.Identity) (identity.Key, error) {
	return performClientHandshake(conn, id, StreamTypeGossip)
}
