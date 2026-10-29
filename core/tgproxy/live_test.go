package tgproxy

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"testing"
	"time"
)

// TestLiveTelegramRoundTrip connects to the proxy the way the Telegram app does and asks DC 2 for
// req_pq_multi. A resPQ answer means the whole chain works: client → proxy → WebSocket to
// Telegram → back. The link Zapretify gives out uses a dd secret (padded intermediate).
// Opt-in, needs the internet: ZAPRETIFY_LIVE_TESTS=1 go test ./core/tgproxy -run Live -v
func TestLiveTelegramRoundTrip(t *testing.T) {
	if os.Getenv("ZAPRETIFY_LIVE_TESTS") == "" {
		t.Skip("set ZAPRETIFY_LIVE_TESTS=1")
	}
	const secretHex = "b48c4c7fc559caa2ec22dd91a2a562a7"
	secret, _ := hex.DecodeString(secretHex)

	for _, tc := range []struct {
		name    string
		fakeTLS bool
		tag     []byte
	}{
		{"dd (ссылка Zapretify)", false, protoTagSecure},
		{"obfuscated2 intermediate", false, protoTagIntermediate},
		{"Fake TLS (ee)", true, protoTagIntermediate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			port := l.Addr().(*net.TCPAddr).Port
			_ = l.Close()
			s := GetServer()
			// Same settings as the app's defaults (Cloudflare fallback and HTTP/2 on).
			if err := s.Start("127.0.0.1", port, secretHex, true, true, nil, nil, false); err != nil {
				t.Fatal(err)
			}
			defer s.Stop()

			raw, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 3*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			_ = raw.SetDeadline(time.Now().Add(30 * time.Second))

			var conn io.ReadWriter = raw
			if tc.fakeTLS {
				if conn, err = clientFakeTLS(raw, secret); err != nil {
					t.Fatalf("fake TLS: %v", err)
				}
			}

			start := time.Now()
			enc, dec := clientObfuscated2(t, conn, secret, tc.tag, 2)
			// Unencrypted MTProto message: auth_key_id 0, message_id, length, req_pq_multi#be7e8ef1 nonce.
			msg := make([]byte, 20+4+16)
			binary.LittleEndian.PutUint64(msg[8:], uint64(time.Now().Unix())<<32)
			binary.LittleEndian.PutUint32(msg[16:], 20)
			binary.LittleEndian.PutUint32(msg[20:], 0xbe7e8ef1)
			_, _ = rand.Read(msg[24:])
			frame := make([]byte, 4+len(msg))
			binary.LittleEndian.PutUint32(frame, uint32(len(msg)))
			copy(frame[4:], msg)
			enc.XORKeyStream(frame, frame)
			if _, err := conn.Write(frame); err != nil {
				t.Fatalf("write: %v", err)
			}

			hdr := make([]byte, 4)
			if _, err := io.ReadFull(conn, hdr); err != nil {
				t.Fatalf("no answer from Telegram: %v", err)
			}
			dec.XORKeyStream(hdr, hdr)
			n := binary.LittleEndian.Uint32(hdr)
			if n < 24 || n > 4096 {
				t.Fatalf("bad answer length %d", n)
			}
			body := make([]byte, n)
			if _, err := io.ReadFull(conn, body); err != nil {
				t.Fatalf("read: %v", err)
			}
			dec.XORKeyStream(body, body)
			if c := binary.LittleEndian.Uint32(body[20:]); c != 0x05162463 {
				t.Fatalf("expected resPQ, got constructor %08x", c)
			}
			t.Logf("resPQ от Telegram DC 2 за %v", time.Since(start).Round(time.Millisecond))
		})
	}
}

// clientFakeTLS sends a ClientHello signed with the secret and returns a connection that frames
// writes and reads as TLS application data, after skipping the server's hello.
func clientFakeTLS(c net.Conn, secret []byte) (*fakeTLSConn, error) {
	body := []byte{0x01, 0, 0, 0, 0x03, 0x03}
	body = append(body, make([]byte, 32)...) // client random, filled below
	body = append(body, 0x20)
	sid := make([]byte, 32)
	_, _ = rand.Read(sid)
	body = append(body, sid...)
	body = append(body, 0x00, 0x02, 0x13, 0x01, 0x01, 0x00) // one cipher suite, no compression
	l := len(body) - 4
	body[1], body[2], body[3] = byte(l>>16), byte(l>>8), byte(l)
	rec := append([]byte{tlsRecordHandshake, 0x03, 0x01, byte(len(body) >> 8), byte(len(body))}, body...)

	mac := hmacSHA256(secret, rec)
	random := rec[tlsClientRandomOffset : tlsClientRandomOffset+32]
	copy(random, mac[:28])
	var ts [4]byte
	binary.LittleEndian.PutUint32(ts[:], uint32(time.Now().Unix()))
	for i := 0; i < 4; i++ {
		random[28+i] = mac[28+i] ^ ts[i]
	}
	if _, err := c.Write(rec); err != nil {
		return nil, err
	}
	// ServerHello record, then CCS and one application data record of random padding.
	hdr := make([]byte, 5)
	if _, err := io.ReadFull(c, hdr); err != nil {
		return nil, fmt.Errorf("server hello: %w", err)
	}
	if _, err := io.CopyN(io.Discard, c, int64(binary.BigEndian.Uint16(hdr[3:5]))); err != nil {
		return nil, fmt.Errorf("server hello body (% x): %w", hdr, err)
	}
	fc := &fakeTLSConn{raw: c}
	if _, err := fc.readTLSPayload(); err != nil {
		return nil, fmt.Errorf("padding record: %w", err)
	}
	return fc, nil
}

// clientObfuscated2 sends the 64-byte obfuscated handshake for the protocol tag and dc,
// returning the client's encrypt and decrypt streams.
func clientObfuscated2(t *testing.T, w io.Writer, secret, tag []byte, dc int16) (enc, dec cipher.Stream) {
	hs := make([]byte, handshakeLen)
	for {
		_, _ = rand.Read(hs)
		if hs[0] != 0xef && hs[0] != tlsRecordHandshake && string(hs[4:8]) != "\x00\x00\x00\x00" {
			break
		}
	}
	copy(hs[protoTagPos:], tag)
	binary.LittleEndian.PutUint16(hs[dcIdxPos:], uint16(dc))

	encKey := sha256.Sum256(append(append([]byte{}, hs[8:40]...), secret...))
	eb, _ := aes.NewCipher(encKey[:])
	enc = cipher.NewCTR(eb, hs[40:56])
	rev := reverseBytes(hs[8:56])
	decKey := sha256.Sum256(append(append([]byte{}, rev[:32]...), secret...))
	db, _ := aes.NewCipher(decKey[:])
	dec = cipher.NewCTR(db, rev[32:48])

	encrypted := make([]byte, handshakeLen)
	enc.XORKeyStream(encrypted, hs)
	copy(hs[56:], encrypted[56:])
	if _, err := w.Write(hs); err != nil {
		t.Fatalf("handshake: %v", err)
	}
	return enc, dec
}
