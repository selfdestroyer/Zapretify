package tgproxy

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"net"
	"strings"
	"testing"
	"time"
)

func TestHandshakeAndRelayInit(t *testing.T) {
	secretHex := "b48c4c7fc559caa2ec22dd91a2a562a7"
	secret, err := hex.DecodeString(secretHex)
	if err != nil {
		t.Fatalf("failed to decode secret: %v", err)
	}

	// Generate relayInit for DC 2 (not media) with intermediate tag
	relayInit := generateRelayInit(protoTagIntermediate, 2)
	if len(relayInit) != handshakeLen {
		t.Fatalf("expected relayInit len %d, got %d", handshakeLen, len(relayInit))
	}

	// Test ciphers initialization
	clientDecPrekeyAndIV := make([]byte, prekeyLen+ivLen)
	copy(clientDecPrekeyAndIV, relayInit[skipLen:skipLen+prekeyLen+ivLen])

	cltDec, cltEnc, tgEnc, tgDec, err := buildCiphers(clientDecPrekeyAndIV, relayInit, secret)
	if err != nil {
		t.Fatalf("buildCiphers failed: %v", err)
	}
	if cltDec == nil || cltEnc == nil || tgEnc == nil || tgDec == nil {
		t.Fatalf("expected non-nil ciphers")
	}
}

func TestMsgSplitterIntermediate(t *testing.T) {
	relayInit := generateRelayInit(protoTagIntermediate, 2)
	ms, err := newMsgSplitter(relayInit, protoIntermediateInt)
	if err != nil {
		t.Fatalf("newMsgSplitter failed: %v", err)
	}

	// Create test payload: 4 bytes length prefix + 12 bytes body = 16 bytes total
	payload := []byte("hello telegram world")
	packet := make([]byte, 4+len(payload))
	binary.LittleEndian.PutUint32(packet[:4], uint32(len(payload)))
	copy(packet[4:], payload)

	// Encrypt packet with ms.dec's cipher stream
	encrypted := make([]byte, len(packet))
	ms.dec.XORKeyStream(encrypted, packet)

	// Reset splitter cipher for test
	msTest, _ := newMsgSplitter(relayInit, protoIntermediateInt)
	parts := msTest.split(encrypted)
	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(parts))
	}
	if !bytes.Equal(parts[0], encrypted) {
		t.Fatalf("expected split part to match original encrypted data")
	}
}

func TestServerLifecycle(t *testing.T) {
	s := GetServer()
	secret := "b48c4c7fc559caa2ec22dd91a2a562a7"

	// Find free port
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	err = s.Start("127.0.0.1", port, secret, false, false, nil, nil, false)
	if err != nil {
		t.Fatalf("failed to start server: %v", err)
	}

	if !s.IsRunning() {
		t.Fatalf("server should report running")
	}

	// Try connecting
	conn, err := net.DialTimeout("tcp", s.listener.Addr().String(), 1*time.Second)
	if err != nil {
		t.Fatalf("failed to connect to server: %v", err)
	}
	_ = conn.Close()

	err = s.Stop()
	if err != nil {
		t.Fatalf("failed to stop server: %v", err)
	}

	if s.IsRunning() {
		t.Fatalf("server should report stopped")
	}
}

func TestSecretPrefixHandling(t *testing.T) {
	// Secret of length 32 starting with dd should NOT be stripped
	sec32dd := "dd123456789abcdef0123456789abcde"
	// Secret of length 32 starting with ee should NOT be stripped
	sec32ee := "ee123456789abcdef0123456789abcde"
	// Secret of length 34 starting with dd SHOULD be stripped
	sec34dd := "dd0123456789abcdef0123456789abcdef"
	// Secret of length 34 starting with ee SHOULD be stripped
	sec34ee := "ee0123456789abcdef0123456789abcdef"

	clean := func(s string) string {
		cleanSecret := strings.TrimSpace(s)
		lowerSecret := strings.ToLower(cleanSecret)
		if len(cleanSecret) == 34 && (strings.HasPrefix(lowerSecret, "dd") || strings.HasPrefix(lowerSecret, "ee")) {
			cleanSecret = cleanSecret[2:]
		}
		if len(cleanSecret) > 32 {
			cleanSecret = cleanSecret[:32]
		}
		return cleanSecret
	}

	if c := clean(sec32dd); c != sec32dd {
		t.Errorf("expected 32-char secret starting with dd to remain %s, got %s", sec32dd, c)
	}
	if c := clean(sec32ee); c != sec32ee {
		t.Errorf("expected 32-char secret starting with ee to remain %s, got %s", sec32ee, c)
	}
	if c := clean(sec34dd); c != "0123456789abcdef0123456789abcdef" {
		t.Errorf("expected 34-char secret starting with dd to be stripped, got %s", c)
	}
	if c := clean(sec34ee); c != "0123456789abcdef0123456789abcdef" {
		t.Errorf("expected 34-char secret starting with ee to be stripped, got %s", c)
	}
}

func TestStopClosesActiveConnections(t *testing.T) {
	s := &Server{}
	secret := "b48c4c7fc559caa2ec22dd91a2a562a7"

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	err = s.Start("127.0.0.1", port, secret, false, false, nil, nil, false)
	if err != nil {
		t.Fatalf("failed to start server: %v", err)
	}

	conn, err := net.DialTimeout("tcp", s.listener.Addr().String(), 1*time.Second)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	// Wait briefly for acceptLoop to store connection
	time.Sleep(50 * time.Millisecond)

	// Stop server
	err = s.Stop()
	if err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	// Verify that conn is severed on Stop: reading from conn should return EOF or error
	_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 16)
	_, err = conn.Read(buf)
	if err == nil {
		t.Errorf("expected read error/EOF after server stop, but read succeeded")
	}
}
