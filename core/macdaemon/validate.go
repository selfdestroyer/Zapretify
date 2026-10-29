package macdaemon

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// allowedOptions are the utunws options the daemon passes on. The engine runs as root, so a strategy
// from the user's strategies.json must not be able to use anything else (for example --debug=@file
// would make root write to any file). A new Flowseal strategy with an unknown option fails to start
// with a clear error instead; the option is added here after checking what it does.
var allowedOptions = map[string]bool{
	"--new":                             true,
	"--filter-l3":                       true,
	"--filter-l7":                       true,
	"--filter-tcp":                      true,
	"--filter-udp":                      true,
	"--hostlist":                        true,
	"--hostlist-domains":                true,
	"--hostlist-exclude":                true,
	"--hostlist-exclude-domains":        true,
	"--ipset":                           true,
	"--ipset-exclude":                   true,
	"--ip-id":                           true,
	"--dpi-desync":                      true,
	"--dpi-desync-any-protocol":         true,
	"--dpi-desync-badseq-increment":     true,
	"--dpi-desync-cutoff":               true,
	"--dpi-desync-fake-discord":         true,
	"--dpi-desync-fake-http":            true,
	"--dpi-desync-fake-quic":            true,
	"--dpi-desync-fake-stun":            true,
	"--dpi-desync-fake-tls":             true,
	"--dpi-desync-fake-tls-mod":         true,
	"--dpi-desync-fake-unknown":         true,
	"--dpi-desync-fake-unknown-udp":     true,
	"--dpi-desync-fakedsplit-pattern":   true,
	"--dpi-desync-fooling":              true,
	"--dpi-desync-hostfakesplit-mod":    true,
	"--dpi-desync-repeats":              true,
	"--dpi-desync-split-pos":            true,
	"--dpi-desync-split-seqovl":         true,
	"--dpi-desync-split-seqovl-pattern": true,
}

// plainValue is what a non-path option value may contain: numbers, words, lists, hex, "!" and
// key=value modifiers (--dpi-desync-fake-tls-mod=rnd,sni=www.google.com).
var plainValue = regexp.MustCompile(`^[A-Za-z0-9_.:,!+=*-]*$`)

// ValidateEngineArgs checks translated strategy arguments before they reach the root engine:
// only allowed options, and every path inside one of dirs (the daemon's own lists/ and bin/).
func ValidateEngineArgs(args []string, dirs ...string) error {
	if len(args) == 0 {
		return fmt.Errorf("пустая стратегия")
	}
	for _, a := range args {
		name, value, hasValue := strings.Cut(a, "=")
		if !allowedOptions[name] {
			return fmt.Errorf("опция %q не разрешена для движка", name)
		}
		if !hasValue {
			continue
		}
		if strings.Contains(value, "/") {
			if !insideDirs(value, dirs) {
				return fmt.Errorf("путь %q вне папок Zapretify", value)
			}
			continue
		}
		if !plainValue.MatchString(value) {
			return fmt.Errorf("недопустимое значение в %q", a)
		}
	}
	return nil
}

func insideDirs(p string, dirs []string) bool {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p {
		return false
	}
	for _, d := range dirs {
		if strings.HasPrefix(p, filepath.Clean(d)+"/") {
			return true
		}
	}
	return false
}

// PFRules builds the pf anchor that sends the strategy's ports to the engine interface.
// Only traffic of regular users is routed (user > root): the engine itself and system daemons are not.
func PFRules(utun, peer string, tcpPorts, udpPorts []string) (string, error) {
	var b strings.Builder
	for _, r := range []struct {
		proto string
		ports []string
	}{{"tcp", tcpPorts}, {"udp", udpPorts}} {
		if len(r.ports) == 0 {
			continue
		}
		list, err := pfPortList(r.ports)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "pass out quick route-to (%s %s) inet proto %s from any to any port {%s} user { >root } no state\n",
			utun, peer, r.proto, list)
	}
	if b.Len() == 0 {
		return "", fmt.Errorf("в стратегии нет портов для перехвата")
	}
	return b.String(), nil
}

// pfPortList turns winws ports ("443", "50000-50100") into pf syntax ("443,50000:50100").
func pfPortList(ports []string) (string, error) {
	out := make([]string, 0, len(ports))
	for _, p := range ports {
		lo, hi, isRange := strings.Cut(p, "-")
		a, err1 := strconv.Atoi(lo)
		b := a
		var err2 error
		if isRange {
			b, err2 = strconv.Atoi(hi)
		}
		if err1 != nil || err2 != nil || a < 1 || b > 65535 || a > b {
			return "", fmt.Errorf("неверный порт %q", p)
		}
		if isRange {
			out = append(out, fmt.Sprintf("%d:%d", a, b))
		} else {
			out = append(out, strconv.Itoa(a))
		}
	}
	return strings.Join(out, ","), nil
}
