package zapret

import (
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
