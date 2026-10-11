package tgproxy

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"net"
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
