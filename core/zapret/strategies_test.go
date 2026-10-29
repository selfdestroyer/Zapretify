package zapret

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildArgsDisabledGameFilter(t *testing.T) {
	raw := `--wf-tcp=80,443,%GameFilterTCP% --wf-udp=443,50000-50050,%GameFilterUDP% --filter-tcp=%GameFilterTCP% --filter-udp=%GameFilterUDP%`
	args := BuildArgs(raw, "bin", "lists", "disabled", "1024-65535", "1024-65535")
	joined := strings.Join(args, " ")

	if !strings.Contains(joined, "--filter-tcp=12") {
		t.Errorf("expected dummy port 12 for disabled GameFilterTCP, got: %s", joined)
	}
	if !strings.Contains(joined, "--filter-udp=12") {
		t.Errorf("expected dummy port 12 for disabled GameFilterUDP, got: %s", joined)
	}
}

func TestBuildArgsUdpGameFilter(t *testing.T) {
	raw := `--wf-tcp=80,443,%GameFilterTCP% --wf-udp=443,50000-50050,%GameFilterUDP% --filter-tcp=%GameFilterTCP% --filter-udp=%GameFilterUDP%`
	args := BuildArgs(raw, "bin", "lists", "udp", "1024-65535", "5000-6000")
	joined := strings.Join(args, " ")

	if !strings.Contains(joined, "--filter-tcp=12") {
		t.Errorf("expected dummy port 12 for GameFilterTCP in UDP mode, got: %s", joined)
	}
	if !strings.Contains(joined, "--filter-udp=5000-6000") {
		t.Errorf("expected custom range 5000-6000 for GameFilterUDP, got: %s", joined)
	}
}

func TestBuildArgsStringEscaping(t *testing.T) {
	raw := `--hostlist="%LISTS%list-general.txt"`
	res := BuildArgsString(raw, `C:\zapret\bin\`, `C:\zapret\lists\`, "disabled", "", "")

	if !strings.Contains(res, `\"C:\zapret\lists\list-general.txt\"`) && !strings.Contains(res, `\"C:/zapret/lists/list-general.txt\"`) {
		t.Errorf("expected escaped quotes in sc args string, got: %s", res)
	}
}

func TestRegistryFindIsStrict(t *testing.T) {
	r := &StrategyRegistry{Strategies: []Strategy{{ID: "general", Name: "General"}, {ID: "alt", Name: "Alt"}}}

	if s, ok := r.Find("alt"); !ok || s.ID != "alt" {
		t.Fatalf("expected to find alt, got %v %v", s, ok)
	}
	if s, ok := r.Find("does-not-exist"); ok || s != nil {
		t.Fatalf("unknown strategy must not be found, got %v", s)
	}
	if s, ok := r.Default(); !ok || s.ID != "general" {
		t.Fatalf("expected default strategy general, got %v %v", s, ok)
	}
	empty := &StrategyRegistry{}
	if _, ok := empty.Default(); ok {
		t.Fatalf("empty registry has no default")
	}
}

func TestBuildArgsUnescapesCaret(t *testing.T) {
	raw := `--dpi-desync-fake-tls=0x00000000 --dpi-desync-fake-tls=^! --hostlist="%LISTS%a^b.txt" --x=^^`
	args := BuildArgs(raw, "bin", "lists", "disabled", "", "")
	want := []string{"--dpi-desync-fake-tls=0x00000000", "--dpi-desync-fake-tls=!", "--hostlist=lists" + string(filepath.Separator) + "a^b.txt", "--x=^"}
	if strings.Join(args, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q, want %q", args, want)
	}
	if res := BuildArgsString(raw, "bin", "lists", "disabled", "", ""); strings.Contains(res, "=^!") {
		t.Fatalf("service args still contain ^!: %s", res)
	}
}
