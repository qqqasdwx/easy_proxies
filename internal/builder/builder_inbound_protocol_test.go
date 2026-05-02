package builder

import (
	"testing"
	"time"

	"easy_proxies/internal/config"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
)

func TestBuildInboundByProtocol(t *testing.T) {
	listenAddr, err := parseAddr("127.0.0.1")
	if err != nil {
		t.Fatalf("parse address: %v", err)
	}

	tests := []struct {
		name     string
		protocol string
		wantType string
	}{
		{name: "default mixed", protocol: "", wantType: C.TypeMixed},
		{name: "http", protocol: config.InboundProtocolHTTP, wantType: C.TypeHTTP},
		{name: "socks5", protocol: config.InboundProtocolSOCKS5, wantType: C.TypeSOCKS},
		{name: "mixed", protocol: config.InboundProtocolMixed, wantType: C.TypeMixed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inbound, err := buildInboundByProtocol(tt.protocol, listenAddr, 2323, "user", "pass", "test-in")
			if err != nil {
				t.Fatalf("build inbound: %v", err)
			}
			if inbound.Type != tt.wantType {
				t.Fatalf("inbound type = %q, want %q", inbound.Type, tt.wantType)
			}
			switch opts := inbound.Options.(type) {
			case *option.HTTPMixedInboundOptions:
				if len(opts.Users) != 1 || opts.Users[0].Username != "user" || opts.Users[0].Password != "pass" {
					t.Fatalf("unexpected HTTP/mixed users: %+v", opts.Users)
				}
			case *option.SocksInboundOptions:
				if len(opts.Users) != 1 || opts.Users[0].Username != "user" || opts.Users[0].Password != "pass" {
					t.Fatalf("unexpected SOCKS users: %+v", opts.Users)
				}
			default:
				t.Fatalf("unexpected inbound options type %T", inbound.Options)
			}
		})
	}
}

func TestBuildUsesConfiguredInboundProtocols(t *testing.T) {
	cfg := &config.Config{
		Mode: "hybrid",
		Listener: config.ListenerConfig{
			Address:  "127.0.0.1",
			Port:     2323,
			Protocol: config.InboundProtocolHTTP,
			Username: "pool-user",
			Password: "pool-pass",
		},
		MultiPort: config.MultiPortConfig{
			Address:  "127.0.0.1",
			BasePort: 24000,
			Protocol: config.InboundProtocolSOCKS5,
			Username: "mp-user",
			Password: "mp-pass",
		},
		Pool: config.PoolConfig{
			Mode:              "sequential",
			FailureThreshold:  3,
			BlacklistDuration: time.Hour,
		},
		Nodes: []config.NodeConfig{{
			Name: "node-1",
			URI:  "http://user:pass@example.com:8080",
			Port: 24000,
		}},
	}

	opts, err := Build(cfg)
	if err != nil {
		t.Fatalf("build options: %v", err)
	}

	var poolInboundFound, multiPortInboundFound bool
	for _, inbound := range opts.Inbounds {
		switch inbound.Tag {
		case "http-in":
			poolInboundFound = true
			if inbound.Type != C.TypeHTTP {
				t.Fatalf("pool inbound type = %q, want %q", inbound.Type, C.TypeHTTP)
			}
		case "in-node-1":
			multiPortInboundFound = true
			if inbound.Type != C.TypeSOCKS {
				t.Fatalf("multi-port inbound type = %q, want %q", inbound.Type, C.TypeSOCKS)
			}
		}
	}
	if !poolInboundFound {
		t.Fatal("pool inbound not found")
	}
	if !multiPortInboundFound {
		t.Fatal("multi-port inbound not found")
	}
}
