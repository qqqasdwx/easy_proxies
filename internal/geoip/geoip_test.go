package geoip

import (
	"net"
	"testing"
)

func TestSelectIPAddrsByStrategy(t *testing.T) {
	ips := []net.IPAddr{
		{IP: net.ParseIP("2001:db8::1")},
		{IP: net.ParseIP("203.0.113.1")},
		{IP: net.ParseIP("2001:db8::2")},
		{IP: net.ParseIP("203.0.113.2")},
	}

	tests := []struct {
		name     string
		strategy string
		want     []string
	}{
		{name: "as is", strategy: "as_is", want: []string{"2001:db8::1", "203.0.113.1", "2001:db8::2", "203.0.113.2"}},
		{name: "prefer ipv4", strategy: "prefer_ipv4", want: []string{"203.0.113.1", "203.0.113.2", "2001:db8::1", "2001:db8::2"}},
		{name: "prefer ipv6", strategy: "prefer_ipv6", want: []string{"2001:db8::1", "2001:db8::2", "203.0.113.1", "203.0.113.2"}},
		{name: "ipv4 only", strategy: "ipv4_only", want: []string{"203.0.113.1", "203.0.113.2"}},
		{name: "ipv6 only", strategy: "ipv6_only", want: []string{"2001:db8::1", "2001:db8::2"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := selectIPAddrsByStrategy(ips, tt.strategy)
			if len(got) != len(tt.want) {
				t.Fatalf("result length = %d, want %d: %+v", len(got), len(tt.want), got)
			}
			for idx, want := range tt.want {
				if got[idx].IP.String() != want {
					t.Fatalf("result[%d] = %s, want %s", idx, got[idx].IP.String(), want)
				}
			}
		})
	}
}

func TestDNSServerAddress(t *testing.T) {
	tests := []struct {
		name   string
		server string
		port   uint16
		want   string
	}{
		{name: "ipv4", server: "1.1.1.1", port: 53, want: "1.1.1.1:53"},
		{name: "host port", server: "dns.example.com:5353", port: 53, want: "dns.example.com:5353"},
		{name: "ipv6", server: "2001:4860:4860::8888", port: 53, want: "[2001:4860:4860::8888]:53"},
		{name: "url", server: "udp://8.8.8.8:5353", port: 53, want: "8.8.8.8:5353"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dnsServerAddress(tt.server, tt.port); got != tt.want {
				t.Fatalf("dnsServerAddress() = %q, want %q", got, tt.want)
			}
		})
	}
}
