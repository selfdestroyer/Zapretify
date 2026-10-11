package tgproxy

import (
	"strings"
)

func defaultCFProxyDomains() []string {
	out := make([]string, 0, len(cfProxyDefaultDomainPool))
	for _, domain := range cfProxyDefaultDomainPool {
		if normalized := normalizeCFProxyDomain(domain); normalized != "" {
			out = appendUniqueDomains(out, normalized)
		}
	}
	return out
}

func normalizeCFProxyDomain(raw string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(raw), "."))
}

func appendUniqueDomains(dst []string, domains ...string) []string {
	for _, domain := range domains {
		normalized := normalizeCFProxyDomain(domain)
		if normalized == "" {
			continue
		}
		duplicate := false
		for _, existing := range dst {
			if existing == normalized {
				duplicate = true
				break
			}
		}
		if !duplicate {
			dst = append(dst, normalized)
		}
	}
	return dst
}
