package config

import (
	"os"
	"strings"
	"testing"
)

func clearManagementEnv(t *testing.T) {
	t.Helper()
	names := []string{
		EnvManagementPort,
		EnvManagementPassword,
	}
	for _, name := range names {
		oldValue, hadValue := os.LookupEnv(name)
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("unset %s: %v", name, err)
		}
		t.Cleanup(func() {
			if hadValue {
				_ = os.Setenv(name, oldValue)
			} else {
				_ = os.Unsetenv(name)
			}
		})
	}
}

func TestDefaultUsesManagementOnlyDefaults(t *testing.T) {
	clearManagementEnv(t)

	cfg, err := Default()
	if err != nil {
		t.Fatalf("default config: %v", err)
	}
	if len(cfg.Nodes) != 0 {
		t.Fatalf("nodes = %d, want 0", len(cfg.Nodes))
	}
	if !cfg.ManagementEnabled() {
		t.Fatal("management should be enabled by default")
	}
	if cfg.Management.Listen != "0.0.0.0:9091" {
		t.Fatalf("management listen = %q, want 0.0.0.0:9091", cfg.Management.Listen)
	}
	if cfg.DatabasePath != "data/data.db" {
		t.Fatalf("database path = %q, want data/data.db", cfg.DatabasePath)
	}
}

func TestManagementEnvOverrides(t *testing.T) {
	clearManagementEnv(t)
	t.Setenv(EnvManagementPort, "19091")
	t.Setenv(EnvManagementPassword, "secret")

	cfg, err := Default()
	if err != nil {
		t.Fatalf("default config: %v", err)
	}
	if cfg.Management.Listen != "0.0.0.0:19091" {
		t.Fatalf("management listen = %q, want 0.0.0.0:19091", cfg.Management.Listen)
	}
	if cfg.Management.Password != "secret" {
		t.Fatal("management password was not read from environment")
	}
}

func TestDefaultInboundProtocolDefaultsToMixed(t *testing.T) {
	clearManagementEnv(t)

	cfg, err := Default()
	if err != nil {
		t.Fatalf("default config: %v", err)
	}

	if cfg.Listener.Protocol != InboundProtocolMixed {
		t.Fatalf("listener protocol = %q, want %q", cfg.Listener.Protocol, InboundProtocolMixed)
	}
	if cfg.MultiPort.Protocol != InboundProtocolMixed {
		t.Fatalf("multi-port protocol = %q, want %q", cfg.MultiPort.Protocol, InboundProtocolMixed)
	}
	if cfg.DatabasePath != "data/data.db" {
		t.Fatalf("database path = %q, want data/data.db", cfg.DatabasePath)
	}
}

func TestNormalizeInboundProtocolNormalizesAliases(t *testing.T) {
	cfg := Config{
		Listener:  ListenerConfig{Protocol: "SOCKS"},
		MultiPort: MultiPortConfig{Protocol: "HTTP"},
		Nodes: []NodeConfig{{
			Name: "node-1",
			URI:  "http://user:pass@example.com:8080",
		}},
	}
	if err := cfg.NormalizeWithPortMap(nil); err != nil {
		t.Fatalf("normalize config: %v", err)
	}

	if cfg.Listener.Protocol != InboundProtocolSOCKS5 {
		t.Fatalf("listener protocol = %q, want %q", cfg.Listener.Protocol, InboundProtocolSOCKS5)
	}
	if cfg.MultiPort.Protocol != InboundProtocolHTTP {
		t.Fatalf("multi-port protocol = %q, want %q", cfg.MultiPort.Protocol, InboundProtocolHTTP)
	}
}

func TestNormalizeInboundProtocolRejectsInvalidValue(t *testing.T) {
	cfg := Config{
		Listener: ListenerConfig{Protocol: "ftp"},
		Nodes: []NodeConfig{{
			Name: "node-1",
			URI:  "http://user:pass@example.com:8080",
		}},
	}
	err := cfg.NormalizeWithPortMap(nil)
	if err == nil {
		t.Fatal("expected invalid protocol error")
	}
	if !strings.Contains(err.Error(), "listener.protocol") {
		t.Fatalf("error = %q, want listener.protocol context", err.Error())
	}
}

func TestNormalizeWithPortMapNormalizesInboundProtocols(t *testing.T) {
	cfg := Config{
		Listener:  ListenerConfig{Protocol: "http"},
		MultiPort: MultiPortConfig{Protocol: "socks"},
		Nodes: []NodeConfig{{
			Name: "node-1",
			URI:  "http://user:pass@example.com:8080",
		}},
	}
	if err := cfg.NormalizeWithPortMap(nil); err != nil {
		t.Fatalf("normalize with port map: %v", err)
	}

	if cfg.Listener.Protocol != InboundProtocolHTTP {
		t.Fatalf("listener protocol = %q, want %q", cfg.Listener.Protocol, InboundProtocolHTTP)
	}
	if cfg.MultiPort.Protocol != InboundProtocolSOCKS5 {
		t.Fatalf("multi-port protocol = %q, want %q", cfg.MultiPort.Protocol, InboundProtocolSOCKS5)
	}
}
