package zapret

import (
	"fmt"
	"strings"
)

// UtunwsConfig is a Flowseal strategy (written for winws.exe) translated for utunws,
// the engine of the macOS port.
type UtunwsConfig struct {
	// Args are the engine arguments; WriteUtunwsConfig writes them one per line for "utunws @file".
	Args []string
	// TCPPorts and UDPPorts come from --wf-tcp/--wf-udp (WinDivert filter): on macOS the daemon
	// routes these ports to utunws with pf. Ranges keep the winws form "50000-50100".
	TCPPorts []string
	UDPPorts []string
}

// TranslateForUtunws converts strategy arguments from strategies.json into the utunws form:
//   - cmd carets are removed and %BIN%/%LISTS% are replaced, as on Windows;
//   - --wf-tcp/--wf-udp are dropped from the arguments and returned as port lists;
//   - profiles (parts between --new) that use %GameFilterTCP%/%GameFilterUDP% are dropped:
//     Game Filter is not supported on macOS yet.
//
// An unknown --wf-* option or an unresolved %VAR% is an error rather than a silently broken profile.
func TranslateForUtunws(rawArgs, binDir, listsDir string) (UtunwsConfig, error) {
	raw := unescapeCaret(rawArgs)
	raw = strings.ReplaceAll(raw, "%BIN%", withSlash(binDir))
	raw = strings.ReplaceAll(raw, "%LISTS%", withSlash(listsDir))

	var cfg UtunwsConfig
	var profiles [][]string
	var cur []string
	skip := false // the current profile uses Game Filter
	flush := func() {
		if len(cur) > 0 && !skip {
			profiles = append(profiles, cur)
		}
		cur, skip = nil, false
	}

	for _, arg := range splitArgs(raw) {
		switch {
		case strings.HasPrefix(arg, "--wf-tcp="):
			cfg.TCPPorts = append(cfg.TCPPorts, portList(strings.TrimPrefix(arg, "--wf-tcp="))...)
			continue
		case strings.HasPrefix(arg, "--wf-udp="):
			cfg.UDPPorts = append(cfg.UDPPorts, portList(strings.TrimPrefix(arg, "--wf-udp="))...)
			continue
		case strings.HasPrefix(arg, "--wf-"):
			return UtunwsConfig{}, fmt.Errorf("опция WinDivert %q не поддерживается на macOS", arg)
		case arg == "--new":
			flush()
			continue
		}
		if strings.Contains(arg, "%GameFilter") {
			// "--filter-tcp=443,%GameFilterTCP%" keeps 443; a filter with only Game Filter ports
			// (or any other option using them) drops the whole profile.
			k, v, _ := strings.Cut(arg, "=")
			ports := portList(v)
			if (k != "--filter-tcp" && k != "--filter-udp") || len(ports) == 0 {
				skip = true
				continue
			}
			arg = k + "=" + strings.Join(ports, ",")
		} else if strings.Count(arg, "%") >= 2 {
			return UtunwsConfig{}, fmt.Errorf("неизвестная переменная в аргументе %q", arg)
		}
		cur = append(cur, arg)
	}
	flush()

	if len(profiles) == 0 {
		return UtunwsConfig{}, fmt.Errorf("в стратегии не осталось профилей для macOS")
	}
	for i, p := range profiles {
		if i > 0 {
			cfg.Args = append(cfg.Args, "--new")
		}
		cfg.Args = append(cfg.Args, p...)
	}
	return cfg, nil
}

// WriteUtunwsConfig renders arguments for "utunws @file": one argument per line,
// values containing spaces quoted the way Flowseal's macOS configs do (--hostlist="/a b/c.txt").
func WriteUtunwsConfig(args []string) string {
	var b strings.Builder
	for _, a := range args {
		if strings.ContainsAny(a, " \t") {
			if k, v, ok := strings.Cut(a, "="); ok && strings.HasPrefix(k, "--") {
				a = k + `="` + v + `"`
			} else {
				a = `"` + a + `"`
			}
		}
		b.WriteString(a)
		b.WriteByte('\n')
	}
	return b.String()
}

// portList splits a --wf-* value, leaving out Game Filter placeholders.
func portList(v string) []string {
	var ports []string
	for _, p := range strings.Split(v, ",") {
		p = strings.TrimSpace(p)
		if p != "" && !strings.Contains(p, "%") {
			ports = append(ports, p)
		}
	}
	return ports
}

func withSlash(dir string) string {
	if dir != "" && !strings.HasSuffix(dir, "/") {
		dir += "/"
	}
	return dir
}
