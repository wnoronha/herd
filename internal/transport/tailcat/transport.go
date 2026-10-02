package tailcat

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/memberlist"
	tcat "github.com/tailscale/tailcat"
	"go4.org/mem"
	"herd/internal/identity"
	"herd/internal/logger"
	"tailscale.com/types/key"
	tslogger "tailscale.com/types/logger"
)

// Config configures the Tailcat Memberlist transport.
type Config struct {
	BindAddr          string
	BindPort          int
	Identity          *identity.Identity
	PacketBufferSize  int
	EnableEncryption  bool
	Logger            logger.Logger
}

// Transport adapts Tailcat's encrypted P2P layer to the memberlist.Transport interface.
type Transport struct {
	config           Config
	udpConn          *net.UDPConn
	tcpListen        *net.TCPListener
	tcServer         *tcat.Server
	tcListen         net.Listener
	tailcatAddr      string
	packetCh         chan *memberlist.Packet
	streamCh         chan net.Conn
	shutdown         atomic.Bool
	wg               sync.WaitGroup
	mu               sync.Mutex
	actualPort       int
	actualIP         net.IP
	peerKeys         map[string]identity.Key
	peerKeysMu       sync.RWMutex
	peerTailcat      map[string]string
	peerTailcatMu    sync.RWMutex
	streamHandlers   map[byte]func(net.Conn)
	streamHandlersMu sync.RWMutex
	packetConns      map[string]*packetConnEntry
	packetConnsMu    sync.Mutex
}

type packetConnEntry struct {
	conn net.Conn
	mu   sync.Mutex
}

type clientConn struct {
	net.Conn
	client *tcat.Client
}

func (c *clientConn) Close() error {
	err1 := c.Conn.Close()
	err2 := c.client.Close()
	if err1 != nil {
		return err1
	}
	return err2
}

// NewTransport initializes the UDP and TCP listeners and returns a memberlist.Transport.
func NewTransport(cfg Config) (*Transport, error) {
	if cfg.Identity == nil {
		return nil, errors.New("identity is required for tailcat transport")
	}
	if cfg.Logger == nil {
		cfg.Logger = logger.NewNop()
	}
	if cfg.PacketBufferSize <= 0 {
		cfg.PacketBufferSize = 1024
	}
	if cfg.BindAddr == "" {
		cfg.BindAddr = "0.0.0.0"
	}

	var udpConn *net.UDPConn
	var tcpListen *net.TCPListener
	var actualPort int

	if cfg.BindPort == 0 {
		var lastErr error
		for attempt := 0; attempt < 25; attempt++ {
			uAddr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(cfg.BindAddr, "0"))
			if err != nil {
				return nil, fmt.Errorf("failed to resolve UDP bind address: %w", err)
			}
			uConn, err := net.ListenUDP("udp", uAddr)
			if err != nil {
				lastErr = err
				continue
			}
			port := uConn.LocalAddr().(*net.UDPAddr).Port
			tAddr, err := net.ResolveTCPAddr("tcp", net.JoinHostPort(cfg.BindAddr, strconv.Itoa(port)))
			if err != nil {
				_ = uConn.Close()
				lastErr = err
				continue
			}
			tListen, err := net.ListenTCP("tcp", tAddr)
			if err != nil {
				_ = uConn.Close()
				lastErr = err
				continue
			}
			udpConn = uConn
			tcpListen = tListen
			actualPort = port
			break
		}
		if udpConn == nil || tcpListen == nil {
			return nil, fmt.Errorf("failed to find free UDP/TCP port pair: %w", lastErr)
		}
	} else {
		udpAddr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(cfg.BindAddr, strconv.Itoa(cfg.BindPort)))
		if err != nil {
			return nil, fmt.Errorf("failed to resolve UDP bind address: %w", err)
		}

		uConn, err := net.ListenUDP("udp", udpAddr)
		if err != nil {
			return nil, fmt.Errorf("failed to listen UDP on %s: %w", udpAddr.String(), err)
		}

		tcpAddr, err := net.ResolveTCPAddr("tcp", net.JoinHostPort(cfg.BindAddr, strconv.Itoa(cfg.BindPort)))
		if err != nil {
			_ = uConn.Close()
			return nil, fmt.Errorf("failed to resolve TCP bind address: %w", err)
		}

		tListen, err := net.ListenTCP("tcp", tcpAddr)
		if err != nil {
			_ = uConn.Close()
			return nil, fmt.Errorf("failed to listen TCP on %s: %w", tcpAddr.String(), err)
		}

		udpConn = uConn
		tcpListen = tListen
		actualPort = cfg.BindPort
	}

	localUDP, _ := udpConn.LocalAddr().(*net.UDPAddr)
	var actualIP net.IP
	if localUDP != nil {
		actualIP = localUDP.IP
		if actualPort == 0 {
			actualPort = localUDP.Port
		}
	}

	t := &Transport{
		config:           cfg,
		udpConn:          udpConn,
		tcpListen:        tcpListen,
		packetCh:         make(chan *memberlist.Packet, cfg.PacketBufferSize),
		streamCh:         make(chan net.Conn, 128),
		actualPort:       actualPort,
		actualIP:         actualIP,
		peerKeys:         make(map[string]identity.Key),
		peerTailcat:      make(map[string]string),
		streamHandlers:   make(map[byte]func(net.Conn)),
		packetConns:      make(map[string]*packetConnEntry),
	}

	// In hermetic test mode, do not connect to external DERP relays or leak network info
	if os.Getenv("IN_TS_TEST") != "" || os.Getenv("HERD_DEV_LOCAL_DERP") != "" {
		t.tailcatAddr = fmt.Sprintf("tcp-mock-%s", cfg.Identity.PublicKey.String()[:16])
	} else {
		// Best-effort startup of Tailcat DERP overlay listener
		tcCtx, tcCancel := context.WithTimeout(context.Background(), 10*time.Second)
		var psk tcat.PresharedKey
		copy(psk[:], cfg.Identity.PreSharedKey[:])
		nodePriv, _ := key.ParseNodePrivateUntyped(mem.S(hex.EncodeToString(cfg.Identity.PrivateKey[:])))

		tcServer := &tcat.Server{
			Key:          nodePriv,
			PresharedKey: psk,
			Logf:         tslogger.Logf(cfg.Logger.Named("tailcat").Logf()),
		}
		tcPort := actualPort
		if tcPort <= 0 {
			tcPort = 7946
		}
		tcListen, tcErr := tcServer.Listen(tcCtx, "tcp", fmt.Sprintf(":%d", tcPort))
		tcCancel()
		if tcErr == nil {
			t.tcServer = tcServer
			t.tcListen = tcListen
			t.tailcatAddr = string(tcServer.TailcatAddr())
			t.wg.Add(1)
			go t.acceptTailcatStreams()
			cfg.Logger.Named("transport").Debugf("tailcat DERP listener started on %s", t.tailcatAddr)
		} else {
			cfg.Logger.Named("transport").Debugf("tailcat DERP listener skipped/failed: %v", tcErr)
		}
	}

	t.wg.Add(2)
	go t.readUDPPackets()
	go t.acceptTCPStreams()

	return t, nil
}

// TailcatIP returns the local Tailcat virtual overlay IP if available.
func (t *Transport) TailcatIP() net.IP {
	if t.tcServer != nil && t.tcServer.Addr().IsValid() {
		return net.ParseIP(t.tcServer.Addr().String())
	}
	return nil
}

// FinalAdvertiseAddr returns the IP and port to advertise to the cluster.
func (t *Transport) FinalAdvertiseAddr(ip string, port int) (net.IP, int, error) {
	advertisePort := t.actualPort
	if port > 0 && port == t.actualPort {
		advertisePort = port
	}

	if t.config.BindAddr != "" && t.config.BindAddr != "0.0.0.0" {
		parsedIP := net.ParseIP(t.config.BindAddr)
		if parsedIP != nil {
			return parsedIP, advertisePort, nil
		}
	}

	if tcIP := t.TailcatIP(); tcIP != nil {
		return tcIP, advertisePort, nil
	}

	if t.actualIP != nil && !t.actualIP.IsUnspecified() {
		return t.actualIP, advertisePort, nil
	}

	// Default to 127.0.0.1 or first non-loopback IP
	addrs, err := net.InterfaceAddrs()
	if err == nil {
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() && ipNet.IP.To4() != nil {
				return ipNet.IP, advertisePort, nil
			}
		}
	}

	return net.IPv4(127, 0, 0, 1), advertisePort, nil
}

func isLoopbackHost(host string) bool {
	if host == "127.0.0.1" || host == "localhost" || host == "::1" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (t *Transport) hasPeerTailcat(addr string) bool {
	host, _, _ := splitHostPortSafe(addr)
	if host != "" && isLoopbackHost(host) {
		return false
	}
	t.peerTailcatMu.RLock()
	defer t.peerTailcatMu.RUnlock()
	if _, ok := t.peerTailcat[addr]; ok {
		return true
	}
	clean := strings.Trim(strings.Trim(addr, "[]"), " ")
	if _, ok := t.peerTailcat[clean]; ok {
		return true
	}
	if host != "" {
		if _, ok := t.peerTailcat[host]; ok {
			return true
		}
	}
	return false
}

// WriteTo writes a packet payload to the given destination address.
func (t *Transport) WriteTo(b []byte, addr string) (time.Time, error) {
	if t.shutdown.Load() {
		return time.Now(), nil
	}

	// For Tailcat overlay addresses (IPv6 or DERP) or registered Tailcat peers,
	// deliver via persistent authenticated stream.
	if strings.Contains(addr, "fd7a:115c:") || strings.HasPrefix(addr, "tcp") || t.hasPeerTailcat(addr) {
		return t.sendPacketStream(addr, b)
	}

	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return t.sendPacketStream(addr, b)
	}

	t.peerKeysMu.RLock()
	remotePub, hasPub := t.peerKeys[addr]
	if !hasPub && udpAddr != nil {
		remotePub, hasPub = t.peerKeys[udpAddr.String()]
	}
	t.peerKeysMu.RUnlock()

	var payload []byte
	if hasPub {
		sharedKey, sErr := DeriveSharedSecret(t.config.Identity.PrivateKey, remotePub)
		if sErr == nil {
			payload, err = EncryptPacketWithKey(t.config.Identity, sharedKey, b)
		}
	}
	if payload == nil || err != nil {
		payload, err = EncryptPacket(t.config.Identity, b)
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to encrypt packet: %w", err)
	}

	now := time.Now()
	_, err = t.udpConn.WriteToUDP(payload, udpAddr)
	if err != nil {
		if t.shutdown.Load() {
			return now, nil
		}
		// If raw UDP write fails, fall back to reliable stream transport
		return t.sendPacketStream(addr, b)
	}
	return now, nil
}

func (t *Transport) sendPacketStream(addr string, b []byte) (time.Time, error) {
	if t.shutdown.Load() {
		return time.Now(), nil
	}

	for attempt := 0; attempt < 2; attempt++ {
		entry, err := t.getOrCreatePacketConn(addr)
		if err != nil {
			if attempt == 0 {
				continue
			}
			return time.Now(), err
		}

		entry.mu.Lock()
		_ = entry.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
		frame := make([]byte, 4+len(b))
		binary.BigEndian.PutUint32(frame[:4], uint32(len(b)))
		copy(frame[4:], b)

		_, err = entry.conn.Write(frame)
		entry.mu.Unlock()

		if err == nil {
			return time.Now(), nil
		}

		_ = entry.conn.Close()
		t.removePacketConn(entry)
	}

	return time.Now(), fmt.Errorf("failed to deliver packet over stream to %s", addr)
}

func (t *Transport) getOrCreatePacketConn(addr string) (*packetConnEntry, error) {
	t.packetConnsMu.Lock()
	entry, ok := t.packetConns[addr]
	if !ok {
		host, _, _ := net.SplitHostPort(addr)
		if host != "" && !isLoopbackHost(host) {
			entry, ok = t.packetConns[host]
		}
	}
	if !ok {
		clean := strings.Trim(strings.Trim(addr, "[]"), " ")
		if !isLoopbackHost(clean) {
			entry, ok = t.packetConns[clean]
		}
	}
	t.packetConnsMu.Unlock()

	if ok && entry != nil && entry.conn != nil {
		return entry, nil
	}

	conn, err := t.DialStream(addr, StreamTypePacket, 5*time.Second)
	if err != nil {
		return nil, err
	}

	entry = &packetConnEntry{conn: conn}
	t.registerPacketConn(addr, entry)

	t.wg.Add(1)
	go t.readPacketStream(entry)

	return entry, nil
}

func (t *Transport) registerPacketConn(addr string, entry *packetConnEntry) {
	t.packetConnsMu.Lock()
	defer t.packetConnsMu.Unlock()
	t.packetConns[addr] = entry
	if entry.conn != nil && entry.conn.RemoteAddr() != nil {
		remoteStr := entry.conn.RemoteAddr().String()
		t.packetConns[remoteStr] = entry
		host, p, err := net.SplitHostPort(remoteStr)
		if err == nil && host != "" && !isLoopbackHost(host) {
			t.packetConns[host] = entry
			if p != "" {
				t.packetConns[net.JoinHostPort(host, p)] = entry
			}
			t.packetConns[net.JoinHostPort(host, strconv.Itoa(t.actualPort))] = entry
		}
	}
	host, p, err := net.SplitHostPort(addr)
	if err == nil && host != "" && !isLoopbackHost(host) {
		t.packetConns[host] = entry
		if p != "" {
			t.packetConns[net.JoinHostPort(host, p)] = entry
		}
		t.packetConns[net.JoinHostPort(host, strconv.Itoa(t.actualPort))] = entry
	}
	clean := strings.Trim(strings.Trim(addr, "[]"), " ")
	if clean != "" && !isLoopbackHost(clean) {
		t.packetConns[clean] = entry
	}
}

func (t *Transport) removePacketConn(entry *packetConnEntry) {
	t.packetConnsMu.Lock()
	defer t.packetConnsMu.Unlock()
	for k, v := range t.packetConns {
		if v == entry {
			delete(t.packetConns, k)
		}
	}
}

func (t *Transport) readPacketStream(entry *packetConnEntry) {
	defer t.wg.Done()
	conn := entry.conn
	defer func() {
		_ = conn.Close()
		t.removePacketConn(entry)
	}()

	remote := conn.RemoteAddr()
	fromAddr := remote
	if remote != nil {
		host, p, err := net.SplitHostPort(remote.String())
		if err == nil && host != "" && strings.Contains(host, "fd7a:115c:") {
			if p == "" {
				p = strconv.Itoa(t.actualPort)
			}
			if udpAddr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(host, p)); err == nil {
				fromAddr = udpAddr
			}
		}
	}

	lenBuf := make([]byte, 4)
	for {
		if t.shutdown.Load() {
			return
		}

		_ = conn.SetReadDeadline(time.Time{})
		if _, err := io.ReadFull(conn, lenBuf); err != nil {
			return
		}

		length := binary.BigEndian.Uint32(lenBuf)
		if length > 65535 {
			return
		}

		payload := make([]byte, length)
		if _, err := io.ReadFull(conn, payload); err != nil {
			return
		}

		now := time.Now()
		pkt := &memberlist.Packet{
			Buf:       payload,
			From:      fromAddr,
			Timestamp: now,
		}

		select {
		case t.packetCh <- pkt:
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// PacketCh returns the channel receiving incoming packets.
func (t *Transport) PacketCh() <-chan *memberlist.Packet {
	return t.packetCh
}

// TailcatAddr returns the full Tailcat DERP overlay address if available, or identity address.
func (t *Transport) TailcatAddr() string {
	if t.tailcatAddr != "" {
		return t.tailcatAddr
	}
	if t.config.Identity != nil {
		return t.config.Identity.TailcatAddr()
	}
	return ""
}

// RegisterPeerTailcat associates a peer node name or address with its Tailcat DERP address.
func (t *Transport) RegisterPeerTailcat(addrOrName, tcAddr string) {
	if addrOrName == "" || tcAddr == "" {
		return
	}
	t.peerTailcatMu.Lock()
	defer t.peerTailcatMu.Unlock()
	t.peerTailcat[addrOrName] = tcAddr
}

func splitHostPortSafe(addr string) (string, string, error) {
	if host, port, err := net.SplitHostPort(addr); err == nil {
		return host, port, nil
	}
	// A bare IPv6 address (no brackets, no port) contains more than one colon.
	// Using LastIndex would mistake the final segment for a port number.
	clean := strings.Trim(addr, "[] ")
	if strings.Count(clean, ":") > 1 {
		return clean, "", nil
	}
	idx := strings.LastIndex(addr, ":")
	if idx > 0 {
		host := strings.Trim(addr[:idx], "[] ")
		port := addr[idx+1:]
		return host, port, nil
	}
	return clean, "", nil
}

// DialStream establishes an encrypted stream connection with a specific StreamType.
func (t *Transport) DialStream(addr string, streamType byte, timeout time.Duration) (net.Conn, error) {
	if t.shutdown.Load() {
		return nil, net.ErrClosed
	}

	var rawConn net.Conn
	var dialErr error

	targetPort := uint16(t.actualPort)
	if targetPort == 0 {
		targetPort = 7946
	}
	tcEndpoint := addr
	if h, pStr, err := splitHostPortSafe(addr); err == nil && pStr != "" {
		if p, err := strconv.ParseUint(pStr, 10, 16); err == nil && p > 0 {
			targetPort = uint16(p)
			tcEndpoint = h
		}
	}

	if strings.HasPrefix(tcEndpoint, "tcp") {
		client := tcat.NewClient(tcat.Addr(tcEndpoint))
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		var conn net.Conn
		conn, dialErr = client.DialTCPPort(ctx, targetPort)
		if dialErr != nil {
			_ = client.Close()
		} else {
			rawConn = &clientConn{Conn: conn, client: client}
		}
	} else {
		t.peerTailcatMu.RLock()
		peerTC, hasTC := t.peerTailcat[addr]
		if !hasTC {
			cleanAddr := strings.Trim(strings.Trim(addr, "[]"), " ")
			peerTC, hasTC = t.peerTailcat[cleanAddr]
		}
		if !hasTC {
			host, _, _ := splitHostPortSafe(addr)
			if host != "" && !isLoopbackHost(host) {
				peerTC, hasTC = t.peerTailcat[host]
			}
		}
		t.peerTailcatMu.RUnlock()

		if hasTC {
			if _, pStr, err := splitHostPortSafe(peerTC); err == nil && pStr != "" {
				if p, err := strconv.ParseUint(pStr, 10, 16); err == nil && p > 0 {
					targetPort = uint16(p)
				}
			}
			if strings.HasPrefix(peerTC, "tcp") {
				client := tcat.NewClient(tcat.Addr(peerTC))
				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				defer cancel()
				var conn net.Conn
				conn, dialErr = client.DialTCPPort(ctx, targetPort)
				if dialErr != nil {
					_ = client.Close()
				} else {
					rawConn = &clientConn{Conn: conn, client: client}
				}
			} else {
				d := net.Dialer{Timeout: timeout}
				rawConn, dialErr = d.Dial("tcp", peerTC)
			}
		} else {
			dialTarget := addr
			if h, p, err := splitHostPortSafe(addr); err == nil && p != "" {
				dialTarget = net.JoinHostPort(h, p)
			}
			d := net.Dialer{Timeout: timeout}
			rawConn, dialErr = d.Dial("tcp", dialTarget)
		}
	}

	if dialErr != nil {
		return nil, fmt.Errorf("failed to dial peer %s: %w", addr, dialErr)
	}

	// Perform mutual identity handshake with streamType
	remotePub, err := performClientHandshake(rawConn, t.config.Identity, streamType)
	if err != nil {
		_ = rawConn.Close()
		return nil, fmt.Errorf("peer handshake failed: %w", err)
	}

	t.peerKeysMu.Lock()
	t.peerKeys[addr] = remotePub
	if rawConn.RemoteAddr() != nil {
		t.peerKeys[rawConn.RemoteAddr().String()] = remotePub
	}
	t.peerKeysMu.Unlock()

	// Derive Curve25519 shared secret
	sharedSecret, err := DeriveSharedSecret(t.config.Identity.PrivateKey, remotePub)
	if err != nil {
		_ = rawConn.Close()
		return nil, fmt.Errorf("failed to derive shared secret: %w", err)
	}

	// Wrap in secure encrypted stream
	secureStream, err := newSecureConn(rawConn, sharedSecret, remotePub)
	if err != nil {
		_ = rawConn.Close()
		return nil, fmt.Errorf("failed to initialize secure stream: %w", err)
	}

	return secureStream, nil
}

// DialTimeout establishes an encrypted TCP stream connection for memberlist gossip.
func (t *Transport) DialTimeout(addr string, timeout time.Duration) (net.Conn, error) {
	return t.DialStream(addr, StreamTypeGossip, timeout)
}

// RegisterStreamHandler registers a dispatch handler for custom stream types (exec, forward, files).
func (t *Transport) RegisterStreamHandler(streamType byte, handler func(net.Conn)) {
	t.streamHandlersMu.Lock()
	defer t.streamHandlersMu.Unlock()
	t.streamHandlers[streamType] = handler
}

// StreamCh returns the channel receiving incoming stream connections.
func (t *Transport) StreamCh() <-chan net.Conn {
	return t.streamCh
}

// InjectStream delivers a stream connection to the memberlist stream channel.
func (t *Transport) InjectStream(conn net.Conn) {
	if conn == nil || t.shutdown.Load() {
		return
	}
	select {
	case t.streamCh <- conn:
	case <-time.After(5 * time.Second):
		_ = conn.Close()
	}
}

// Shutdown closes the transport listeners and stops background workers.
func (t *Transport) Shutdown() error {
	if !t.shutdown.CompareAndSwap(false, true) {
		return nil
	}

	t.packetConnsMu.Lock()
	for _, entry := range t.packetConns {
		if entry != nil && entry.conn != nil {
			_ = entry.conn.Close()
		}
	}
	t.packetConns = make(map[string]*packetConnEntry)
	t.packetConnsMu.Unlock()

	t.mu.Lock()
	var errUDP, errTCP, errTC error
	if t.udpConn != nil {
		errUDP = t.udpConn.Close()
	}
	if t.tcpListen != nil {
		errTCP = t.tcpListen.Close()
	}
	if t.tcListen != nil {
		errTC = t.tcListen.Close()
	}
	if t.tcServer != nil {
		_ = t.tcServer.Close()
	}
	t.mu.Unlock()

	t.wg.Wait()

	return errors.Join(errUDP, errTCP, errTC)
}

// GetPort returns the bound port.
func (t *Transport) GetPort() int {
	return t.actualPort
}

func (t *Transport) readUDPPackets() {
	defer t.wg.Done()
	buf := make([]byte, 65535)

	for {
		n, remoteAddr, err := t.udpConn.ReadFrom(buf)
		if err != nil {
			if t.shutdown.Load() {
				return
			}
			continue
		}

		packetData := make([]byte, n)
		copy(packetData, buf[:n])
		now := time.Now()

		// Attempt decryption with our private key (ECDH) or fallback to PSK
		plaintext, senderPub, err := DecryptPacketWithPrivKey(t.config.Identity.PrivateKey, packetData)
		if err != nil {
			plaintext, senderPub, err = DecryptPacket(t.config.Identity.PreSharedKey, packetData)
		}
		if err != nil {
			// Drop unauthorized / corrupt packet
			continue
		}

		if remoteAddr != nil {
			t.peerKeysMu.Lock()
			t.peerKeys[remoteAddr.String()] = senderPub
			t.peerKeysMu.Unlock()
		}

		pkt := &memberlist.Packet{
			Buf:       plaintext,
			From:      remoteAddr,
			Timestamp: now,
		}

		select {
		case t.packetCh <- pkt:
		default:
			// Dropped if buffer is full
		}
	}
}

func (t *Transport) acceptTCPStreams() {
	defer t.wg.Done()

	for {
		conn, err := t.tcpListen.Accept()
		if err != nil {
			if t.shutdown.Load() {
				return
			}
			continue
		}
		go t.handleStreamConn(conn)
	}
}

func (t *Transport) acceptTailcatStreams() {
	defer t.wg.Done()

	for {
		if t.tcListen == nil {
			return
		}
		conn, err := t.tcListen.Accept()
		if err != nil {
			if t.shutdown.Load() {
				return
			}
			continue
		}
		go t.handleStreamConn(conn)
	}
}

func (t *Transport) handleStreamConn(c net.Conn) {
	// Server-side handshake reads streamType
	remotePub, streamType, err := performServerHandshake(c, t.config.Identity)
	if err != nil {
		_ = c.Close()
		return
	}

	if c.RemoteAddr() != nil {
		t.peerKeysMu.Lock()
		t.peerKeys[c.RemoteAddr().String()] = remotePub
		t.peerKeysMu.Unlock()
	}

	sharedSecret, err := DeriveSharedSecret(t.config.Identity.PrivateKey, remotePub)
	if err != nil {
		_ = c.Close()
		return
	}

	secConn, err := newSecureConn(c, sharedSecret, remotePub)
	if err != nil {
		_ = c.Close()
		return
	}

	t.streamHandlersMu.RLock()
	handler, hasHandler := t.streamHandlers[streamType]
	t.streamHandlersMu.RUnlock()

	if hasHandler && handler != nil {
		go handler(secConn)
		return
	}

	if streamType == StreamTypePacket {
		entry := &packetConnEntry{conn: secConn}
		if secConn.RemoteAddr() != nil {
			t.registerPacketConn(secConn.RemoteAddr().String(), entry)
		}
		t.wg.Add(1)
		go t.readPacketStream(entry)
		return
	}

	select {
	case t.streamCh <- secConn:
	case <-time.After(5 * time.Second):
		_ = secConn.Close()
	}
}

// RegisterPeerKey associates a remote peer address or identifier with its public key.
func (t *Transport) RegisterPeerKey(addrOrName string, pubKey identity.Key) {
	if addrOrName == "" {
		return
	}
	t.peerKeysMu.Lock()
	defer t.peerKeysMu.Unlock()
	t.peerKeys[addrOrName] = pubKey
}

// LookupPeerKey retrieves the registered public key for an address or identifier if present.
func (t *Transport) LookupPeerKey(addrOrName string) (identity.Key, bool) {
	t.peerKeysMu.RLock()
	defer t.peerKeysMu.RUnlock()
	k, ok := t.peerKeys[addrOrName]
	return k, ok
}
