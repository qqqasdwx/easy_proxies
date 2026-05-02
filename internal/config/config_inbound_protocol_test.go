package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

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

func TestLoadMissingConfigUsesManagementOnlyDefaults(t *testing.T) {
	clearManagementEnv(t)

	cfg, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("load missing config: %v", err)
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
	if cfg.DatabasePath != filepath.Join("data", "data.db") {
		t.Fatalf("database path = %q, want data/data.db", cfg.DatabasePath)
	}
}

func TestManagementEnvOverrides(t *testing.T) {
	clearManagementEnv(t)
	t.Setenv(EnvManagementPort, "19091")
	t.Setenv(EnvManagementPassword, "secret")

	cfg, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("load missing config: %v", err)
	}
	if cfg.Management.Listen != "0.0.0.0:19091" {
		t.Fatalf("management listen = %q, want 0.0.0.0:19091", cfg.Management.Listen)
	}
	if cfg.Management.Password != "secret" {
		t.Fatal("management password was not read from environment")
	}
}

func TestLoadInboundProtocolDefaultsToMixed(t *testing.T) {
	path := writeTestConfig(t, `
mode: pool
nodes:
  - name: node-1
    uri: http://user:pass@example.com:8080
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.Listener.Protocol != InboundProtocolMixed {
		t.Fatalf("listener protocol = %q, want %q", cfg.Listener.Protocol, InboundProtocolMixed)
	}
	if cfg.MultiPort.Protocol != InboundProtocolMixed {
		t.Fatalf("multi-port protocol = %q, want %q", cfg.MultiPort.Protocol, InboundProtocolMixed)
	}

	wantDatabasePath := filepath.Join(filepath.Dir(path), "data", "data.db")
	if cfg.DatabasePath != wantDatabasePath {
		t.Fatalf("database path = %q, want %q", cfg.DatabasePath, wantDatabasePath)
	}
}

func TestLoadInboundProtocolNormalizesAliases(t *testing.T) {
	cfg, err := Load(writeTestConfig(t, `
mode: pool
listener:
  protocol: SOCKS
multi_port:
  protocol: HTTP
nodes:
  - name: node-1
    uri: http://user:pass@example.com:8080
`))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.Listener.Protocol != InboundProtocolSOCKS5 {
		t.Fatalf("listener protocol = %q, want %q", cfg.Listener.Protocol, InboundProtocolSOCKS5)
	}
	if cfg.MultiPort.Protocol != InboundProtocolHTTP {
		t.Fatalf("multi-port protocol = %q, want %q", cfg.MultiPort.Protocol, InboundProtocolHTTP)
	}
}

func TestLoadInboundProtocolRejectsInvalidValue(t *testing.T) {
	_, err := Load(writeTestConfig(t, `
mode: pool
listener:
  protocol: ftp
nodes:
  - name: node-1
    uri: http://user:pass@example.com:8080
`))
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
