package builder

import (
	"strings"
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

func TestBuildUsesPerNodeInboundProtocolAndOutboundJSON(t *testing.T) {
	cfg := &config.Config{
		Mode: "multi-port",
		MultiPort: config.MultiPortConfig{
			Address:  "127.0.0.1",
			BasePort: 24000,
			Protocol: config.InboundProtocolHTTP,
		},
		Pool: config.PoolConfig{
			Mode:              "sequential",
			FailureThreshold:  3,
			BlacklistDuration: time.Hour,
		},
		Nodes: []config.NodeConfig{{
			Name:            "json-node",
			URI:             "json://outbound/test",
			OutboundJSON:    `{"type":"socks","tag":"ignored","server":"127.0.0.1","server_port":1080}`,
			Port:            24000,
			InboundProtocol: config.InboundProtocolSOCKS5,
		}},
	}

	opts, err := Build(cfg)
	if err != nil {
		t.Fatalf("build options: %v", err)
	}

	var foundOutbound, foundInbound bool
	for _, outbound := range opts.Outbounds {
		if outbound.Tag == "json-node" {
			foundOutbound = true
			if outbound.Type != C.TypeSOCKS {
				t.Fatalf("outbound type = %q, want %q", outbound.Type, C.TypeSOCKS)
			}
		}
	}
	for _, inbound := range opts.Inbounds {
		if inbound.Tag == "in-json-node" {
			foundInbound = true
			if inbound.Type != C.TypeSOCKS {
				t.Fatalf("inbound type = %q, want %q", inbound.Type, C.TypeSOCKS)
			}
		}
	}
	if !foundOutbound || !foundInbound {
		t.Fatalf("found outbound=%v inbound=%v", foundOutbound, foundInbound)
	}
}

func TestNormalizeOutboundJSONPreservesOptions(t *testing.T) {
	normalized, err := NormalizeOutboundJSON("json-node", `{
		"type": "socks",
		"tag": "ignored",
		"server": "127.0.0.1",
		"server_port": 1080,
		"version": "5",
		"username": "user",
		"password": "pass"
	}`)
	if err != nil {
		t.Fatalf("normalize outbound json: %v", err)
	}
	for _, want := range []string{
		`"tag": "json-node"`,
		`"server": "127.0.0.1"`,
		`"server_port": 1080`,
		`"username": "user"`,
		`"password": "pass"`,
	} {
		if !strings.Contains(normalized, want) {
			t.Fatalf("normalized JSON missing %s:\n%s", want, normalized)
		}
	}
}

func TestBuildSkipsDisabledNodes(t *testing.T) {
	cfg := &config.Config{
		Mode: "pool",
		Listener: config.ListenerConfig{
			Address:  "127.0.0.1",
			Port:     2323,
			Protocol: config.InboundProtocolMixed,
		},
		Pool: config.PoolConfig{
			Mode:              "sequential",
			FailureThreshold:  3,
			BlacklistDuration: time.Hour,
		},
		Nodes: []config.NodeConfig{
			{
				Name:     "disabled-node",
				URI:      "http://user:pass@disabled.example.com:8080",
				Disabled: true,
			},
			{
				Name: "enabled-node",
				URI:  "http://user:pass@enabled.example.com:8080",
			},
		},
	}

	opts, err := Build(cfg)
	if err != nil {
		t.Fatalf("build options: %v", err)
	}

	for _, outbound := range opts.Outbounds {
		if outbound.Tag == "disabled-node" {
			t.Fatal("disabled node outbound was built")
		}
	}
}
