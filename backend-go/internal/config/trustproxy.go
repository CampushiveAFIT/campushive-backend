package config

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

type trustMode int

const (
	trustNone trustMode = iota
	trustHops
	trustLoopback
	trustLinkLocal
	trustUniqueLocal
	trustAll
)

var trustLoopbackCIDRs = mustCIDRs("127.0.0.0/8", "::1/128")
var trustLinkLocalCIDRs = mustCIDRs("169.254.0.0/16", "fe80::/10")
var trustUniqueLocalCIDRs = mustCIDRs("10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7")

func mustCIDRs(cidrs ...string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(cidrs))
	for _, c := range cidrs {
		p, err := netip.ParsePrefix(c)
		if err != nil {
			panic(err)
		}
		out = append(out, p)
	}
	return out
}

type TrustProxy struct {
	mode trustMode
	hops int
}

func ParseTrustProxy(raw string) (TrustProxy, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	switch v {
	case "", "loopback":
		return TrustProxy{mode: trustLoopback}, nil
	case "false", "none", "off", "0":
		return TrustProxy{mode: trustNone}, nil
	case "true", "always", "on":
		return TrustProxy{mode: trustAll}, nil
	case "linklocal":
		return TrustProxy{mode: trustLinkLocal}, nil
	case "uniquelocal":
		return TrustProxy{mode: trustUniqueLocal}, nil
	}
	if hops, err := strconv.Atoi(v); err == nil {
		if hops < 0 {
			return TrustProxy{}, fmt.Errorf("TRUST_PROXY must not be negative, got %d", hops)
		}
		if hops == 0 {
			return TrustProxy{mode: trustNone}, nil
		}
		return TrustProxy{mode: trustHops, hops: hops}, nil
	}
	return TrustProxy{}, fmt.Errorf("TRUST_PROXY must be an integer hop count or one of loopback, linklocal, uniquelocal, true, false")
}

func (t TrustProxy) ClientIP(remoteAddr string, forwardedFor string) string {
	peer := parseAddr(remoteAddr)
	if t.mode == trustNone {
		return peerOrUnknown(peer)
	}
	chain := splitForwardedFor(forwardedFor)
	if len(chain) == 0 {
		return peerOrUnknown(peer)
	}
	switch t.mode {
	case trustAll:
		return chain[0].String()
	case trustHops:
		if t.hops > len(chain) {
			return chain[0].String()
		}
		return chain[len(chain)-t.hops].String()
	default:
		if !peerIn(peer, t.cidrs()) {
			return peerOrUnknown(peer)
		}
		return chain[0].String()
	}
}

func peerOrUnknown(addr netip.Addr) string {
	if !addr.IsValid() {
		return unknownIP
	}
	return addr.String()
}

func (t TrustProxy) cidrs() []netip.Prefix {
	switch t.mode {
	case trustLoopback:
		return trustLoopbackCIDRs
	case trustLinkLocal:
		return trustLinkLocalCIDRs
	case trustUniqueLocal:
		return trustUniqueLocalCIDRs
	default:
		return nil
	}
}

func peerIn(addr netip.Addr, prefixes []netip.Prefix) bool {
	if !addr.IsValid() {
		return false
	}
	for _, p := range prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

func parseAddr(remoteAddr string) netip.Addr {
	if remoteAddr == "" {
		return netip.Addr{}
	}
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		remoteAddr = host
	}
	remoteAddr = strings.Trim(remoteAddr, "[]")
	addr, err := netip.ParseAddr(remoteAddr)
	if err != nil {
		return netip.Addr{}
	}
	if addr.Is4In6() {
		addr = addr.Unmap()
	}
	return addr
}

func splitForwardedFor(raw string) []netip.Addr {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]netip.Addr, 0, len(parts))
	for _, part := range parts {
		addr := parseAddr(strings.TrimSpace(part))
		if !addr.IsValid() {
			return nil
		}
		out = append(out, addr)
	}
	return out
}

const unknownIP = "unknown"

var ErrInvalidClientIP = errors.New("unable to determine client ip")
