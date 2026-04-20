// Package security provides SSRF-oriented URL validation and path sandboxing.
package security

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// Whitelist is a set of host strings that bypass SSRF checks. Populated from
// config.tools.ssrfWhitelist at startup.
type Whitelist struct {
	hosts map[string]struct{}
}

// NewWhitelist constructs a Whitelist.
func NewWhitelist(hosts []string) *Whitelist {
	m := make(map[string]struct{}, len(hosts))
	for _, h := range hosts {
		m[strings.ToLower(strings.TrimSpace(h))] = struct{}{}
	}
	return &Whitelist{hosts: m}
}

// Allows reports whether the host is explicitly whitelisted.
func (w *Whitelist) Allows(host string) bool {
	if w == nil {
		return false
	}
	_, ok := w.hosts[strings.ToLower(host)]
	return ok
}

// ValidateURL resolves the URL's host and ensures no resulting IP is a private,
// link-local, loopback, or metadata address (unless whitelisted).
func ValidateURL(ctx context.Context, raw string, w *Whitelist) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse url: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("missing host")
	}
	if w.Allows(host) {
		return nil
	}
	resolver := net.DefaultResolver
	ips, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("lookup host: %w", err)
	}
	for _, ip := range ips {
		if isDisallowed(ip.IP) {
			return fmt.Errorf("host %q resolves to blocked IP %s", host, ip.IP)
		}
	}
	return nil
}

// ValidateResolved ensures a single already-resolved IP is acceptable. Used
// after redirects.
func ValidateResolved(ip net.IP, w *Whitelist) error {
	if w != nil {
		// whitelist is host-based; skip IP-level bypass
	}
	if isDisallowed(ip) {
		return fmt.Errorf("ip %s is disallowed", ip)
	}
	return nil
}

func isDisallowed(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	if ip.IsMulticast() {
		return true
	}
	if ip.IsPrivate() {
		return true
	}
	// AWS/GCE/Azure metadata addresses
	if ip.Equal(net.ParseIP("169.254.169.254")) || ip.Equal(net.ParseIP("fd00:ec2::254")) {
		return true
	}
	return false
}

// ContainsInternalURL checks whether a free-form command string looks like it
// embeds a URL pointing at disallowed infrastructure. Used by the shell tool
// to refuse commands such as `curl http://169.254.169.254/...`.
func ContainsInternalURL(s string) bool {
	// cheap scan for "169.254.169.254", "localhost", and "127.0.0.1" appearing
	// after an "://". Anything more sophisticated belongs in the tools layer.
	lower := strings.ToLower(s)
	for _, needle := range []string{"169.254.169.254", "://localhost", "://127.", "://[::1]", "://10.", "://192.168.", "://172."} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}
