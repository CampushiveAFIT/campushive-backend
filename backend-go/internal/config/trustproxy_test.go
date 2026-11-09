package config

import "testing"

func TestTrustProxyClientIP(t *testing.T) {
	cases := []struct {
		name       string
		trust      string
		remoteAddr string
		forwarded  string
		want       string
	}{
		{"loopback peer trusts forwarded chain", "loopback", "127.0.0.1:5000", "203.0.113.7, 10.0.0.1", "203.0.113.7"},
		{"public peer ignores forwarded chain", "loopback", "203.0.113.9:5000", "198.51.100.4", "203.0.113.9"},
		{"default is loopback", "", "127.0.0.1:1234", "198.51.100.9", "198.51.100.9"},
		{"never trusts", "false", "127.0.0.1:1234", "198.51.100.9", "127.0.0.1"},
		{"zero hops is never", "0", "127.0.0.1:1234", "198.51.100.9", "127.0.0.1"},
		{"two hops from right", "2", "10.0.0.5:1234", "198.51.100.1, 203.0.113.2, 10.0.0.3", "203.0.113.2"},
		{"one hop from right", "1", "10.0.0.5:1234", "198.51.100.1, 203.0.113.2", "203.0.113.2"},
		{"always trusts", "true", "10.0.0.5:1234", "198.51.100.1, 203.0.113.2", "198.51.100.1"},
		{"linklocal peer", "linklocal", "169.254.10.10:1234", "198.51.100.1", "198.51.100.1"},
		{"uniquelocal peer", "uniquelocal", "192.168.1.10:1234", "198.51.100.1", "198.51.100.1"},
		{"ipv6 loopback peer", "loopback", "[::1]:1234", "2001:db8::5", "2001:db8::5"},
		{"ipv4 mapped ipv6 peer", "loopback", "[::ffff:127.0.0.1]:1234", "198.51.100.1", "198.51.100.1"},
		{"no forwarded header", "loopback", "127.0.0.1:1234", "", "127.0.0.1"},
		{"garbage forwarded header", "loopback", "127.0.0.1:1234", "not-an-ip", "127.0.0.1"},
		{"unix socket remote", "loopback", "/tmp/backend.sock", "", "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			trust, err := ParseTrustProxy(tc.trust)
			if err != nil {
				t.Fatalf("ParseTrustProxy(%q): %v", tc.trust, err)
			}
			if got := trust.ClientIP(tc.remoteAddr, tc.forwarded); got != tc.want {
				t.Errorf("ClientIP = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTrustProxyRejectsInvalid(t *testing.T) {
	for _, raw := range []string{"-1", "2.5", "yes-ish"} {
		if _, err := ParseTrustProxy(raw); err == nil {
			t.Errorf("ParseTrustProxy(%q) should fail", raw)
		}
	}
}
