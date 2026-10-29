package platform

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func ips(s ...string) []net.IP {
	out := make([]net.IP, 0, len(s))
	for _, x := range s {
		out = append(out, net.ParseIP(x))
	}
	return out
}

func TestJudgeDNS(t *testing.T) {
	strict := dnsProbe{"discord.com", true}
	loose := dnsProbe{"youtube.com", false}
	secure := ips("162.159.137.232", "162.159.128.233")
	notFound := &net.DNSError{Err: "no such host", Name: "discord.com", IsNotFound: true}

	cases := []struct {
		name    string
		p       dnsProbe
		sys     []net.IP
		err     error
		spoofed bool
	}{
		{"genuine", strict, ips("162.159.135.232"), nil, false},
		{"zero stub", strict, ips("0.0.0.0"), nil, true},
		{"loopback stub", loose, ips("127.0.0.1"), nil, true},
		{"private stub", loose, ips("10.10.34.35"), nil, true},
		{"provider page", strict, ips("95.167.13.50"), nil, true},
		{"regional google", loose, ips("173.194.73.136"), nil, false},
		{"nxdomain", loose, nil, notFound, true},
		{"timeout is not spoofing", strict, nil, errors.New("i/o timeout"), false},
	}
	for _, c := range cases {
		if got := judgeDNS(c.p, c.sys, c.err, secure) != ""; got != c.spoofed {
			t.Errorf("%s: spoofed=%v, want %v", c.name, got, c.spoofed)
		}
	}
}

func TestCheckDNS(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/dns-json" {
			t.Errorf("missing Accept header")
		}
		ip := map[string]string{"discord.com": "162.159.137.232", "youtube.com": "142.250.74.14", "instagram.com": "157.240.205.174"}[r.URL.Query().Get("name")]
		fmt.Fprintf(w, `{"Status":0,"Answer":[{"type":5,"data":"x."},{"type":1,"data":%q}]}`, ip)
	}))
	defer srv.Close()

	oldEP, oldLookup, oldTransport := dohEndpoints, systemLookup, http.DefaultTransport
	defer func() { dohEndpoints, systemLookup, http.DefaultTransport = oldEP, oldLookup, oldTransport }()
	// The first endpoint is unreachable, so the check must fall back to the second one.
	dohEndpoints = []string{"https://127.0.0.1:1/dns-query", srv.URL + "/resolve"}
	http.DefaultTransport = srv.Client().Transport
	systemLookup = func(_ context.Context, host string) ([]net.IP, error) {
		switch host {
		case "discord.com":
			return ips("0.0.0.0"), nil
		case "instagram.com":
			return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
		}
		return ips("142.251.1.100"), nil
	}

	res := CheckDNS()
	if !res.Checked {
		t.Fatal("check was not performed")
	}
	if len(res.Spoofed) != 2 || res.Spoofed[0].Domain != "discord.com" || res.Spoofed[1].Domain != "instagram.com" {
		t.Fatalf("spoofed = %+v", res.Spoofed)
	}
	if res.Spoofed[0].Secure[0] != "162.159.137.232" || res.Spoofed[0].System[0] != "0.0.0.0" {
		t.Errorf("addresses = %+v", res.Spoofed[0])
	}
}

func TestCheckDNSUnreachable(t *testing.T) {
	oldEP := dohEndpoints
	defer func() { dohEndpoints = oldEP }()
	dohEndpoints = []string{"https://127.0.0.1:1/dns-query"}
	if res := CheckDNS(); res.Checked || len(res.Spoofed) != 0 {
		t.Errorf("result = %+v, want unchecked", res)
	}
}

func TestIsVPNAdapter(t *testing.T) {
	vpn := [][2]string{
		{"Ethernet 3", "TAP-Windows Adapter V9"},
		{"WireGuard Tunnel", "WireGuard Tunnel"},
		{"AmneziaVPN", "Wintun Userspace Tunnel"},
		{"Outline-Tap0", "TAP-Windows Adapter V9 for Outline"},
		{"singbox_tun", "sing-tun Tunnel"},
		{"tun0", ""},
		{"Hiddify", "HiddifyTunnel"},
	}
	for _, a := range vpn {
		if !isVPNAdapter(a[0], a[1]) {
			t.Errorf("%v not detected as VPN", a)
		}
	}
	plain := [][2]string{
		{"Ethernet", "Realtek PCIe GbE Family Controller"},
		{"Беспроводная сеть", "Intel(R) Wi-Fi 6 AX201 160MHz"},
		{"vEthernet (Default Switch)", "Hyper-V Virtual Ethernet Adapter"},
		{"Teredo Tunneling Pseudo-Interface", "Microsoft Teredo Tunneling Adapter"},
		{"ZeroTier One", "ZeroTier Virtual Port"},
		{"VirtualBox Host-Only Network", "VirtualBox Host-Only Ethernet Adapter"},
	}
	for _, a := range plain {
		if isVPNAdapter(a[0], a[1]) {
			t.Errorf("%v detected as VPN", a)
		}
	}
}

func TestLiveCheckDNS(t *testing.T) {
	if os.Getenv("ZAPRETIFY_LIVE_TESTS") == "" {
		t.Skip("set ZAPRETIFY_LIVE_TESTS=1 to query real DNS")
	}
	t.Logf("%+v", CheckDNS())
}
