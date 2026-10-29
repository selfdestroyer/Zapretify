package macdaemon

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"zapretify/core/zapret"
)

const (
	testLists = "/Library/Application Support/Zapretify/lists"
	testBin   = "/Library/Application Support/Zapretify/bin"
)

// Every strategy that ships with Zapretify must pass the daemon's checks after translation.
func TestBundledStrategiesPassValidation(t *testing.T) {
	data, err := os.ReadFile("../../strategies.json")
	if err != nil {
		t.Skipf("strategies.json: %v", err)
	}
	var file struct{ Strategies []zapret.Strategy }
	if err := json.Unmarshal(data, &file); err != nil || len(file.Strategies) == 0 {
		t.Fatalf("strategies.json: %v", err)
	}
	for _, s := range file.Strategies {
		cfg, err := zapret.TranslateForUtunws(s.RawArgs, testBin, testLists)
		if err != nil {
			t.Errorf("%s: %v", s.ID, err)
			continue
		}
		if err := ValidateEngineArgs(cfg.Args, testLists, testBin); err != nil {
			t.Errorf("%s: %v", s.ID, err)
		}
		if _, err := PFRules("en0", "utun60", "10.78.0.2", cfg.TCPPorts, cfg.UDPPorts); err != nil {
			t.Errorf("%s: %v", s.ID, err)
		}
	}
}

func TestValidateEngineArgsRejects(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"--debug=@/etc/sudoers"},
		{"@/tmp/evil.conf"},
		{"--hostlist=/etc/master.passwd"},
		{"--hostlist=" + testLists + "/../../../../etc/hosts"},
		{"--hostlist=relative/list.txt"},
		{"--dpi-desync-fake-tls=$(id)"},
		{"--filter-tcp=443", "--wf-tcp=80"},
	} {
		if err := ValidateEngineArgs(args, testLists, testBin); err == nil {
			t.Errorf("expected an error for %q", args)
		}
	}
	ok := []string{"--filter-tcp=443", "--hostlist=" + testLists + "/list-general.txt",
		"--dpi-desync-fake-tls-mod=rnd,dupsid,sni=www.google.com", "--dpi-desync-fake-tls=!", "--new", "--dpi-desync-any-protocol"}
	if err := ValidateEngineArgs(ok, testLists, testBin); err != nil {
		t.Errorf("valid args rejected: %v", err)
	}
}

func TestPFRules(t *testing.T) {
	rules, err := PFRules("en0", "utun60", "10.78.0.2", []string{"80", "443"}, []string{"443", "50000-50100"})
	if err != nil {
		t.Fatal(err)
	}
	want := "pass out quick on en0 route-to (utun60 10.78.0.2) inet proto tcp from any to any port {80,443} user { >root } no state\n" +
		"pass out quick on en0 route-to (utun60 10.78.0.2) inet proto udp from any to any port {443,50000:50100} user { >root } no state\n"
	if rules != want {
		t.Fatalf("got:\n%swant:\n%s", rules, want)
	}
	for _, bad := range [][]string{{"0"}, {"70000"}, {"100-50"}, {"80;pass"}, {"443 any"}} {
		if _, err := PFRules("en0", "utun60", "10.78.0.2", bad, nil); err == nil {
			t.Errorf("expected an error for %q", bad)
		}
	}
	if _, err := PFRules("en0", "utun60", "10.78.0.2", nil, nil); err == nil || !strings.Contains(err.Error(), "портов") {
		t.Errorf("expected an error for no ports, got %v", err)
	}
}
