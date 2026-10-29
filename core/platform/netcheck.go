package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// ForeignProcess is a running process that is not part of this Zapretify installation.
type ForeignProcess struct {
	PID  int    `json:"pid"`
	Path string `json:"path"` // empty when the image path could not be read
}

// DNSSpoof describes a domain the system resolver answers differently from encrypted DNS.
type DNSSpoof struct {
	Domain string   `json:"domain"`
	System []string `json:"system"`
	Secure []string `json:"secure"`
	Reason string   `json:"reason"`
}

// DNSCheck is the result of comparing the system resolver with DNS-over-HTTPS.
type DNSCheck struct {
	Checked bool       `json:"checked"` // false when encrypted DNS was unreachable for every domain
	Spoofed []DNSSpoof `json:"spoofed"`
}

// dnsProbe is a domain checked for spoofing. Strict domains are served from anycast addresses
// that are the same worldwide, so an answer outside them points to a provider stub.
// Other domains (Google, Meta) legitimately resolve to regional addresses and are only checked
// for stub and "no such domain" answers.
type dnsProbe struct {
	domain string
	strict bool
}

var dnsProbes = []dnsProbe{
	{"discord.com", true},
	{"youtube.com", false},
	{"instagram.com", false},
}

// dohEndpoints are JSON DNS-over-HTTPS APIs tried in order; overridable in tests.
var dohEndpoints = []string{
	"https://1.1.1.1/dns-query",
	"https://dns.google/resolve",
}

// systemLookup resolves through the operating system resolver; overridable in tests.
var systemLookup = func(ctx context.Context, host string) ([]net.IP, error) {
	return net.DefaultResolver.LookupIP(ctx, "ip4", host)
}

const dnsCheckTimeout = 6 * time.Second

var errNoSuchDomain = errors.New("no such domain")

// lookupDoH resolves A records of host through the first reachable DoH endpoint.
func lookupDoH(ctx context.Context, client *http.Client, host string) ([]net.IP, error) {
	var lastErr error
	for _, ep := range dohEndpoints {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, ep+"?name="+url.QueryEscape(host)+"&type=A", nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/dns-json")
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		var body struct {
			Status int `json:"Status"`
			Answer []struct {
				Type int    `json:"type"`
				Data string `json:"data"`
			} `json:"Answer"`
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("%s: HTTP %d %v", ep, resp.StatusCode, err)
			continue
		}
		if body.Status == 3 {
			return nil, errNoSuchDomain
		}
		var ips []net.IP
		for _, a := range body.Answer {
			if a.Type != 1 {
				continue // CNAME and other records
			}
			if ip := net.ParseIP(a.Data).To4(); ip != nil {
				ips = append(ips, ip)
			}
		}
		if len(ips) == 0 {
			lastErr = fmt.Errorf("%s: пустой ответ", ep)
			continue
		}
		return ips, nil
	}
	return nil, lastErr
}

var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0).To4(), Mask: net.CIDRMask(10, 32)}

// isStubIP reports addresses that can never be a public website: what providers return
// instead of a blocked domain.
func isStubIP(ip net.IP) bool {
	return ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || cgnat.Contains(ip)
}

func sameSlash16(a, b []net.IP) bool {
	for _, x := range a {
		for _, y := range b {
			x4, y4 := x.To4(), y.To4()
			if x4 != nil && y4 != nil && x4[0] == y4[0] && x4[1] == y4[1] {
				return true
			}
		}
	}
	return false
}

func ipStrings(ips []net.IP) []string {
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		out = append(out, ip.String())
	}
	sort.Strings(out)
	return out
}

// judgeDNS compares the system answer with the encrypted one and returns the reason
// the system answer looks spoofed, or "" when it looks genuine.
func judgeDNS(p dnsProbe, sys []net.IP, sysErr error, secure []net.IP) string {
	if sysErr != nil {
		var dnsErr *net.DNSError
		if errors.As(sysErr, &dnsErr) && dnsErr.IsNotFound {
			return "системный DNS отвечает, что домена не существует"
		}
		return "" // a timeout or a network error is not evidence of spoofing
	}
	for _, ip := range sys {
		if isStubIP(ip) {
			return "системный DNS возвращает адрес-заглушку " + ip.String()
		}
	}
	if p.strict && !sameSlash16(sys, secure) {
		return "системный DNS возвращает чужие адреса вместо настоящих"
	}
	return ""
}

// CheckDNS resolves well-known blocked domains through the system resolver and through
// DNS-over-HTTPS, which a provider cannot tamper with, and reports domains whose answers differ.
func CheckDNS() DNSCheck {
	ctx, cancel := context.WithTimeout(context.Background(), dnsCheckTimeout)
	defer cancel()
	client := &http.Client{Timeout: dnsCheckTimeout}

	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		result = DNSCheck{Spoofed: []DNSSpoof{}}
	)
	for _, p := range dnsProbes {
		wg.Add(1)
		go func(p dnsProbe) {
			defer wg.Done()
			secure, err := lookupDoH(ctx, client, p.domain)
			if err != nil {
				return // nothing trustworthy to compare with
			}
			sys, sysErr := systemLookup(ctx, p.domain)
			reason := judgeDNS(p, sys, sysErr, secure)
			mu.Lock()
			defer mu.Unlock()
			result.Checked = true
			if reason != "" {
				result.Spoofed = append(result.Spoofed, DNSSpoof{
					Domain: p.domain, System: ipStrings(sys), Secure: ipStrings(secure), Reason: reason,
				})
			}
		}(p)
	}
	wg.Wait()
	sort.Slice(result.Spoofed, func(i, j int) bool { return result.Spoofed[i].Domain < result.Spoofed[j].Domain })
	return result
}

var (
	vpnAdapterRe = regexp.MustCompile(`wintun|wireguard|tap-windows|tap-proton|openvpn|vpn|(^|[^a-z])tun[0-9]*([^a-z]|$)|tunnel|tun2socks|amnezia|outline|hiddify|nekoray|nekobox|v2ray|xray|clash|sing-?box|mihomo|happ`)
	// Built-in and virtualisation adapters that never carry the user's internet traffic away.
	notVPNAdapterRe = regexp.MustCompile(`teredo|isatap|6to4|ip-https|loopback|hyper-v|vethernet|virtualbox|vmware|bluetooth|wan miniport|zerotier|hamachi`)
)

// isVPNAdapter decides from an adapter's name and description whether it belongs to a VPN or
// a proxy client running in TUN mode.
func isVPNAdapter(name, description string) bool {
	s := strings.ToLower(name + " " + description)
	return vpnAdapterRe.MatchString(s) && !notVPNAdapterRe.MatchString(s)
}
