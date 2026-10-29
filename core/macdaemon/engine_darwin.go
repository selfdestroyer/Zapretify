package macdaemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"zapretify/core/zapret"
)

// Network setup of the engine. Unit, addresses and anchor differ from Flowseal's ZapretMac
// (utun50, 10.77.0.x, com.apple/zapret-macos) so the two programs never touch each other's state.
const (
	utunUnit  = 61 // utunws creates utun<unit-1>
	utunName  = "utun60"
	utunLocal = "10.78.0.1"
	utunPeer  = "10.78.0.2"
	pfAnchor  = "com.apple/zapretify" // pf.conf of macOS loads anchors under com.apple/*
	confPath  = "/var/run/zapretify.conf"

	engineStartTimeout = 10 * time.Second
	engineGrace        = 700 * time.Millisecond
	engineStopTimeout  = 3 * time.Second
	routePollInterval  = 2 * time.Second
	maxLogLines        = 30
)

var macAddr = regexp.MustCompile(`^([0-9a-fA-F]{1,2}:){5}[0-9a-fA-F]{1,2}$`)

// engineProc is one running utunws.
type engineProc struct {
	cmd      *exec.Cmd
	done     chan struct{} // closed when the process has exited
	strategy string
	iface    string
	gateway  string
	pfToken  string
}

// enginePath is the utunws binary installed with the daemon.
func enginePath(rootDir string) string { return filepath.Join(rootDir, "engine", "utunws") }

// startEngine imports the user's bases, translates the strategy and brings up utunws, utun and pf.
// Called with s.mu held and no engine running.
func (s *Server) startEngine(strategyID, userDataDir string) error {
	if pids := foreignEngines(enginePath(s.RootDir)); len(pids) > 0 {
		return fmt.Errorf("уже работает другой движок utunws (PID %v), например ZapretMac. Остановите его и повторите", pids)
	}

	bases, err := ImportBases(userDataDir, s.RootDir)
	if err != nil {
		return err
	}
	var file struct{ Strategies []zapret.Strategy }
	if err := json.Unmarshal(bases.Strategies, &file); err != nil {
		return fmt.Errorf("strategies.json: %w", err)
	}
	var raw string
	found := false
	for _, st := range file.Strategies {
		if st.ID == strategyID {
			raw, found = st.RawArgs, true
			break
		}
	}
	if !found {
		return fmt.Errorf("стратегия %q не найдена", strategyID)
	}
	cfg, err := zapret.TranslateForUtunws(raw, bases.BinDir, bases.ListsDir)
	if err != nil {
		return err
	}
	if err := ValidateEngineArgs(cfg.Args, bases.ListsDir, bases.BinDir); err != nil {
		return err
	}
	rules, err := PFRules(utunName, utunPeer, cfg.TCPPorts, cfg.UDPPorts)
	if err != nil {
		return err
	}
	if err := os.WriteFile(confPath, []byte(zapret.WriteUtunwsConfig(cfg.Args)), 0600); err != nil {
		return err
	}
	return s.launch(strategyID, rules)
}

// launch starts utunws for the config already written to confPath and routes traffic to it.
func (s *Server) launch(strategyID, rules string) error {
	iface, gateway, err := defaultRoute()
	if err != nil {
		return err
	}
	gwMAC, err := neighborMAC(gateway)
	if err != nil {
		return err
	}

	cmd := exec.Command(enginePath(s.RootDir), "@"+confPath)
	cmd.Env = []string{
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin",
		"ZAPRET_IFACE=" + iface,
		"ZAPRET_GATEWAY_MAC=" + gwMAC,
		"ZAPRET_GATEWAY6_MAC=" + gateway6MAC(),
		"ZAPRET_UTUN_UNIT=" + strconv.Itoa(utunUnit),
	}
	s.log.reset()
	cmd.Stdout = s.log
	cmd.Stderr = s.log
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("запуск utunws: %w", err)
	}
	p := &engineProc{cmd: cmd, done: make(chan struct{}), strategy: strategyID, iface: iface, gateway: gateway}
	go func() {
		err := cmd.Wait()
		close(p.done)
		s.engineExited(p, err)
	}()

	fail := func(err error) error {
		s.teardown(p)
		if tail := s.log.tail(5); len(tail) > 0 {
			err = fmt.Errorf("%w: %s", err, strings.Join(tail, " | "))
		}
		return err
	}
	if err := waitInterface(utunName, p.done); err != nil {
		return fail(err)
	}
	if err := run("ifconfig", utunName, utunLocal, utunPeer, "netmask", "255.255.255.255", "up"); err != nil {
		return fail(err)
	}
	if p.pfToken, err = pfEnable(); err != nil {
		return fail(err)
	}
	if err := runInput(rules, "pfctl", "-a", pfAnchor, "-f", "-"); err != nil {
		return fail(fmt.Errorf("правила pf: %w", err))
	}
	select {
	case <-p.done:
		return fail(errors.New("utunws завершился сразу после запуска"))
	case <-time.After(engineGrace):
	}

	s.proc = p
	s.rules = rules
	s.lastExit = ""
	go s.watchRoute(p)
	return nil
}

// teardown removes pf rules, releases pf and stops the process. Safe to call more than once.
func (s *Server) teardown(p *engineProc) {
	_ = run("pfctl", "-a", pfAnchor, "-F", "all")
	if p.pfToken != "" {
		_ = run("pfctl", "-X", p.pfToken)
		p.pfToken = ""
	}
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-p.done:
		case <-time.After(engineStopTimeout):
			_ = p.cmd.Process.Kill()
			<-p.done
		}
	}
}

// stopEngine stops the running engine. Called with s.mu held.
func (s *Server) stopEngine() {
	if s.proc == nil {
		return
	}
	p := s.proc
	s.proc = nil // engineExited sees that the exit was requested
	s.teardown(p)
}

// engineExited is called when utunws ends; an exit nobody asked for removes pf rules so the
// traffic is not sent to a dead interface.
func (s *Server) engineExited(p *engineProc, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.proc != p {
		return
	}
	s.proc = nil
	s.teardown(p)
	msg := "utunws завершился"
	if err != nil {
		msg += ": " + err.Error()
	}
	s.lastExit = msg
	s.logf("%s", msg)
}

// watchRoute restarts the engine when the default route changes (Wi-Fi ↔ Ethernet, another network):
// utunws sends packets to the gateway MAC it got at start.
func (s *Server) watchRoute(p *engineProc) {
	t := time.NewTicker(routePollInterval)
	defer t.Stop()
	for {
		select {
		case <-p.done:
			return
		case <-t.C:
		}
		iface, gw, err := defaultRoute()
		if err == nil && iface == p.iface && gw == p.gateway {
			continue
		}
		s.mu.Lock()
		if s.proc != p {
			s.mu.Unlock()
			return
		}
		s.logf("сеть изменилась (%s %s → %s %s), перезапуск движка", p.iface, p.gateway, iface, gw)
		s.stopEngine()
		if err == nil {
			if lerr := s.launch(p.strategy, s.rules); lerr != nil {
				s.lastExit = "перезапуск после смены сети: " + lerr.Error()
				s.logf("%s", s.lastExit)
			}
		} else {
			s.lastExit = "нет сети: " + err.Error()
		}
		s.mu.Unlock()
		return
	}
}

// cleanupStale removes what a crashed daemon may have left: our pf rules and our utunws.
func cleanupStale(rootDir string) {
	_ = run("pfctl", "-a", pfAnchor, "-F", "all")
	_ = run("pkill", "-TERM", "-f", "^"+regexp.QuoteMeta(enginePath(rootDir))+" ")
}

// foreignEngines lists utunws processes that are not ours (ZapretMac and the like).
func foreignEngines(ours string) []int {
	out, err := exec.Command("pgrep", "-x", "utunws").Output()
	if err != nil {
		return nil
	}
	var pids []int
	for _, f := range strings.Fields(string(out)) {
		pid, err := strconv.Atoi(f)
		if err != nil {
			continue
		}
		args, _ := exec.Command("ps", "-o", "args=", "-p", f).Output()
		if !strings.HasPrefix(strings.TrimSpace(string(args)), ours+" ") {
			pids = append(pids, pid)
		}
	}
	return pids
}

func defaultRoute() (iface, gateway string, err error) {
	out, err := exec.Command("route", "-n", "get", "default").Output()
	if err != nil {
		return "", "", fmt.Errorf("нет маршрута по умолчанию: %w", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		switch k {
		case "interface":
			iface = strings.TrimSpace(v)
		case "gateway":
			gateway = strings.TrimSpace(v)
		}
	}
	if iface == "" || gateway == "" {
		return "", "", errors.New("нет маршрута по умолчанию")
	}
	if strings.HasPrefix(iface, "utun") || strings.HasPrefix(iface, "ppp") || strings.HasPrefix(iface, "ipsec") {
		return "", "", fmt.Errorf("трафик идёт через VPN (%s); выключите VPN", iface)
	}
	return iface, gateway, nil
}

func neighborMAC(gateway string) (string, error) {
	_ = exec.Command("ping", "-c", "1", "-t", "1", gateway).Run() // fill the ARP cache
	out, _ := exec.Command("arp", "-n", gateway).Output()
	// "? (10.1.52.1) at 0:11:22:33:44:55 on en0 ifscope [ethernet]"
	f := strings.Fields(string(out))
	for i := 0; i+1 < len(f); i++ {
		if f[i] == "at" && macAddr.MatchString(f[i+1]) {
			return f[i+1], nil
		}
	}
	return "", fmt.Errorf("не удалось узнать MAC-адрес шлюза %s", gateway)
}

func gateway6MAC() string {
	out, err := exec.Command("route", "-n", "get", "-inet6", "default").Output()
	if err != nil {
		return ""
	}
	var gw string
	for _, line := range strings.Split(string(out), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), ":"); ok && k == "gateway" {
			gw = strings.TrimSpace(v)
		}
	}
	if gw == "" {
		return ""
	}
	out, _ = exec.Command("ndp", "-n", gw).Output()
	for _, f := range strings.Fields(string(out)) {
		if macAddr.MatchString(f) {
			return f
		}
	}
	return ""
}

func waitInterface(name string, exited <-chan struct{}) error {
	deadline := time.Now().Add(engineStartTimeout)
	for time.Now().Before(deadline) {
		if exec.Command("ifconfig", name).Run() == nil {
			return nil
		}
		select {
		case <-exited:
			return errors.New("utunws завершился до создания интерфейса")
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("интерфейс %s не появился за %s", name, engineStartTimeout)
}

// pfEnable takes a reference on pf (enabling it if needed); pfctl -X with the token releases it.
func pfEnable() (string, error) {
	out, err := exec.Command("pfctl", "-E").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("pfctl -E: %v: %s", err, bytes.TrimSpace(out))
	}
	for _, line := range strings.Split(string(out), "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(k) == "Token" {
			return strings.TrimSpace(v), nil
		}
	}
	return "", nil
}

func run(name string, args ...string) error { return runInput("", name, args...) }

func runInput(stdin, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %v: %s", name, err, bytes.TrimSpace(out))
	}
	return nil
}

// ringLog keeps the last engine output lines and mirrors them to the daemon's stderr (launchd log).
type ringLog struct {
	mu    sync.Mutex
	buf   []byte
	lines []string
}

func (l *ringLog) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = os.Stderr.Write(b)
	l.buf = append(l.buf, b...)
	for {
		i := bytes.IndexByte(l.buf, '\n')
		if i < 0 {
			break
		}
		if line := strings.TrimRight(string(l.buf[:i]), "\r"); line != "" {
			l.lines = append(l.lines, line)
			if len(l.lines) > maxLogLines {
				l.lines = l.lines[len(l.lines)-maxLogLines:]
			}
		}
		l.buf = l.buf[i+1:]
	}
	if len(l.buf) > 64<<10 {
		l.buf = nil
	}
	return len(b), nil
}

func (l *ringLog) reset() {
	l.mu.Lock()
	l.buf, l.lines = nil, nil
	l.mu.Unlock()
}

func (l *ringLog) tail(n int) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n > len(l.lines) {
		n = len(l.lines)
	}
	return append([]string(nil), l.lines[len(l.lines)-n:]...)
}
