package tailcat

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"herd/internal/identity"
	"herd/internal/logger"
)

func init() {
	_ = os.Setenv("IN_TS_TEST", "true")
	_ = os.Setenv("HERD_DEV_LOCAL_DERP", "1")
	_ = os.Setenv("TS_DISABLE_UPNP", "true")
	_ = os.Setenv("TS_DEBUG_NETCHECK", "0")
}

func TestCryptoPacket(t *testing.T) {
	id, err := identity.NewIdentity("node1")
	if err != nil {
		t.Fatalf("NewIdentity error: %v", err)
	}

	payload := []byte("hello herd gossip cluster")
	encrypted, err := EncryptPacket(id, payload)
	if err != nil {
		t.Fatalf("EncryptPacket error: %v", err)
	}

	decrypted, senderPub, err := DecryptPacketWithPrivKey(id.PrivateKey, encrypted)
	if err != nil {
		t.Fatalf("DecryptPacket error: %v", err)
	}

	if !bytes.Equal(decrypted, payload) {
		t.Errorf("decrypted %s != original %s", decrypted, payload)
	}
	if senderPub != id.PublicKey {
		t.Errorf("sender pubkey mismatch")
	}

	// Test with PSK encryption
	psk, _ := identity.GeneratePSK()
	encPSK, err := EncryptPacketWithKey(id, psk, payload)
	if err != nil {
		t.Fatalf("EncryptPacketWithKey error: %v", err)
	}
	decPSK, _, err := DecryptPacket(psk, encPSK)
	if err != nil || !bytes.Equal(decPSK, payload) {
		t.Errorf("expected PSK decryption success, got err: %v", err)
	}

	// Test with wrong PSK
	wrongPSK, _ := identity.GeneratePSK()
	_, _, err = DecryptPacket(wrongPSK, encPSK)
	if err == nil {
		t.Errorf("expected decryption failure with wrong PSK")
	}
}

func TestTransportPacketAndStream(t *testing.T) {
	psk, err := identity.GeneratePSK()
	if err != nil {
		t.Fatalf("GeneratePSK error: %v", err)
	}

	id1, _ := identity.NewIdentity("node1")
	id1.PreSharedKey = psk

	id2, _ := identity.NewIdentity("node2")
	id2.PreSharedKey = psk

	t1, err := NewTransport(Config{
		BindAddr: "127.0.0.1",
		BindPort: 0,
		Identity: id1,
	})
	if err != nil {
		t.Fatalf("NewTransport t1 error: %v", err)
	}
	defer func() { _ = t1.Shutdown() }()

	t2, err := NewTransport(Config{
		BindAddr: "127.0.0.1",
		BindPort: 0,
		Identity: id2,
	})
	if err != nil {
		t.Fatalf("NewTransport t2 error: %v", err)
	}
	defer func() { _ = t2.Shutdown() }()

	// 1. Test WriteTo packet delivery from t1 to t2
	t2Addr := fmt.Sprintf("127.0.0.1:%d", t2.GetPort())
	packetMsg := []byte("gossip-ping-12345")

	_, err = t1.WriteTo(packetMsg, t2Addr)
	if err != nil {
		t.Fatalf("t1.WriteTo error: %v", err)
	}

	select {
	case pkt := <-t2.PacketCh():
		if !bytes.Equal(pkt.Buf, packetMsg) {
			t.Errorf("expected packet %s, got %s", packetMsg, pkt.Buf)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for packet on t2")
	}

	// 2. Test TCP DialTimeout / StreamCh streaming
	connCh := make(chan net.Conn, 1)
	go func() {
		streamConn := <-t2.StreamCh()
		connCh <- streamConn
	}()

	clientConn, err := t1.DialTimeout(t2Addr, 2*time.Second)
	if err != nil {
		t.Fatalf("t1.DialTimeout error: %v", err)
	}
	defer func() { _ = clientConn.Close() }()

	var serverConn net.Conn
	select {
	case serverConn = <-connCh:
		defer func() { _ = serverConn.Close() }()
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for stream connection on t2")
	}

	streamData := []byte("streaming state sync block payload")
	go func() {
		_, _ = clientConn.Write(streamData)
	}()

	readBuf := make([]byte, len(streamData))
	_, err = io.ReadFull(serverConn, readBuf)
	if err != nil {
		t.Fatalf("serverConn read error: %v", err)
	}

	if !bytes.Equal(readBuf, streamData) {
		t.Errorf("stream data mismatch: got %s, expected %s", readBuf, streamData)
	}
}

func TestTailcatAddressDeterministicAcrossRestarts(t *testing.T) {
	id, err := identity.NewIdentity("persistent-node")
	if err != nil {
		t.Fatalf("failed to create identity: %v", err)
	}

	// First startup
	t1, err := NewTransport(Config{
		BindAddr: "127.0.0.1",
		BindPort: 0,
		Identity: id,
	})
	if err != nil {
		t.Fatalf("failed to create transport t1: %v", err)
	}
	addr1 := t1.TailcatAddr()
	_ = t1.Shutdown()

	if addr1 == "" {
		t.Fatalf("expected non-empty TailcatAddr from t1")
	}

	// Second startup with identical Identity
	t2, err := NewTransport(Config{
		BindAddr: "127.0.0.1",
		BindPort: 0,
		Identity: id,
	})
	if err != nil {
		t.Fatalf("failed to create transport t2: %v", err)
	}
	addr2 := t2.TailcatAddr()
	_ = t2.Shutdown()

	if addr1 != addr2 {
		t.Fatalf("Tailcat address changed across restarts!\nAddr1: %s\nAddr2: %s", addr1, addr2)
	}
}

func TestStreamPacketDatagramTransport(t *testing.T) {
	id1, err := identity.NewIdentity("test-packet-node-1")
	if err != nil {
		t.Fatalf("failed to create id1: %v", err)
	}
	id2, err := identity.NewIdentity("test-packet-node-2")
	if err != nil {
		t.Fatalf("failed to create id2: %v", err)
	}

	t1, err := NewTransport(Config{
		BindAddr: "127.0.0.1",
		BindPort: 0,
		Identity: id1,
	})
	if err != nil {
		t.Fatalf("NewTransport t1 error: %v", err)
	}
	defer func() { _ = t1.Shutdown() }()

	t2, err := NewTransport(Config{
		BindAddr: "127.0.0.1",
		BindPort: 0,
		Identity: id2,
	})
	if err != nil {
		t.Fatalf("NewTransport t2 error: %v", err)
	}
	defer func() { _ = t2.Shutdown() }()

	t2Addr := fmt.Sprintf("127.0.0.1:%d", t2.GetPort())
	t1Addr := fmt.Sprintf("127.0.0.1:%d", t1.GetPort())

	// Register virtual overlay addresses mapping to each other's TCP endpoints
	virtualT2 := "fd7a:115c:a1e0:beef::2:7946"
	virtualT1 := "fd7a:115c:a1e0:beef::1:7946"
	t1.RegisterPeerTailcat(virtualT2, t2Addr)
	t2.RegisterPeerTailcat(virtualT1, t1Addr)

	// Send packet from t1 to t2 over simulated overlay
	pingMsg := []byte("swim-ping-overlay-test")
	_, err = t1.WriteTo(pingMsg, virtualT2)
	if err != nil {
		t.Fatalf("t1.WriteTo failed: %v", err)
	}

	select {
	case pkt := <-t2.PacketCh():
		if !bytes.Equal(pkt.Buf, pingMsg) {
			t.Fatalf("expected packet %s, got %s", pingMsg, pkt.Buf)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for packet on t2")
	}

	// Send ack back from t2 to t1
	ackMsg := []byte("swim-ack-overlay-test")
	_, err = t2.WriteTo(ackMsg, virtualT1)
	if err != nil {
		t.Fatalf("t2.WriteTo failed: %v", err)
	}

	select {
	case pkt := <-t1.PacketCh():
		if !bytes.Equal(pkt.Buf, ackMsg) {
			t.Fatalf("expected packet %s, got %s", ackMsg, pkt.Buf)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for ack packet on t1")
	}
}

func TestTransport_CustomLogger(t *testing.T) {
	log, err := logger.NewLogger("debug", "json")
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}

	id, err := identity.NewIdentity("lognode")
	if err != nil {
		t.Fatalf("failed to create identity: %v", err)
	}

	tr, err := NewTransport(Config{
		BindAddr: "127.0.0.1",
		BindPort: 0,
		Identity: id,
		Logger:   log,
	})
	if err != nil {
		t.Fatalf("NewTransport error: %v", err)
	}
	defer func() { _ = tr.Shutdown() }()

	if tr.GetPort() == 0 {
		t.Fatalf("expected valid bound port")
	}
}

