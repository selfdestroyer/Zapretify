package zapret

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestTranslateForUtunws(t *testing.T) {
	raw := `--wf-tcp=80,443,%GameFilterTCP% --wf-udp=443,50000-50100,%GameFilterUDP% ` +
		`--filter-udp=443 --hostlist="%LISTS%list-general.txt" --dpi-desync=fake --dpi-desync-fake-quic="%BIN%quic.bin" --new ` +
		`--filter-tcp=443,%GameFilterTCP% --dpi-desync-fake-tls=^! --new ` +
		`--filter-tcp=%GameFilterTCP% --ipset="%LISTS%ipset-all.txt" --dpi-desync=fake --new ` +
		`--filter-udp=%GameFilterUDP% --dpi-desync-fake-unknown-udp="%BIN%game.bin"`

	cfg, err := TranslateForUtunws(raw, "/Library/Application Support/Zapretify/bin", "/Library/Application Support/Zapretify/lists/")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--filter-udp=443",
		"--hostlist=/Library/Application Support/Zapretify/lists/list-general.txt",
		"--dpi-desync=fake",
		"--dpi-desync-fake-quic=/Library/Application Support/Zapretify/bin/quic.bin",
		"--new",
		"--filter-tcp=443",
		"--dpi-desync-fake-tls=!",
	}
	if strings.Join(cfg.Args, "|") != strings.Join(want, "|") {
		t.Fatalf("args:\n got %q\nwant %q", cfg.Args, want)
	}
	if strings.Join(cfg.TCPPorts, ",") != "80,443" || strings.Join(cfg.UDPPorts, ",") != "443,50000-50100" {
		t.Fatalf("ports: tcp %v udp %v", cfg.TCPPorts, cfg.UDPPorts)
	}

	file := WriteUtunwsConfig(cfg.Args)
	if !strings.Contains(file, "--hostlist=\"/Library/Application Support/Zapretify/lists/list-general.txt\"\n") ||
		!strings.Contains(file, "--dpi-desync-fake-tls=!\n") {
		t.Fatalf("config file:\n%s", file)
	}
}

func TestTranslateForUtunwsRejects(t *testing.T) {
	for _, raw := range []string{
		`--wf-l3=ipv4 --filter-tcp=443 --dpi-desync=fake`,
		`--filter-tcp=443 --hostlist="%UNKNOWN%list.txt"`,
		`--wf-tcp=%GameFilterTCP% --filter-tcp=%GameFilterTCP% --dpi-desync=fake`,
	} {
		if _, err := TranslateForUtunws(raw, "/bin", "/lists"); err == nil {
			t.Errorf("expected an error for %q", raw)
		}
	}
}

// Every strategy that ships with Zapretify must translate.
func TestTranslateBundledStrategies(t *testing.T) {
	data, err := os.ReadFile("../../strategies.json")
	if err != nil {
		t.Skipf("strategies.json: %v", err)
	}
	var file struct{ Strategies []Strategy }
	if err := json.Unmarshal(data, &file); err != nil || len(file.Strategies) == 0 {
		t.Fatalf("strategies.json: %v", err)
	}
	for _, s := range file.Strategies {
		cfg, err := TranslateForUtunws(s.RawArgs, "/b", "/l")
		if err != nil {
			t.Errorf("%s: %v", s.ID, err)
			continue
		}
		if len(cfg.TCPPorts) == 0 || len(cfg.UDPPorts) == 0 {
			t.Errorf("%s: no pf ports (tcp %v, udp %v)", s.ID, cfg.TCPPorts, cfg.UDPPorts)
		}
	}
}
