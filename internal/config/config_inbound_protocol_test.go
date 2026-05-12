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

func TestManagementCannotBeDisabledByPersistedConfig(t *testing.T) {
	clearManagementEnv(t)

	disabled := false
	cfg := &Config{Management: ManagementConfig{Enabled: &disabled, Listen: "127.0.0.1:18080"}}
	if err := cfg.NormalizeWithPortMap(nil); err != nil {
		t.Fatalf("normalize config: %v", err)
	}
	if !cfg.ManagementEnabled() {
		t.Fatal("management should always be enabled")
	}
	if cfg.Management.Enabled == nil || !*cfg.Management.Enabled {
		t.Fatal("normalized management enabled flag should be true")
	}
	if cfg.Management.Listen != "0.0.0.0:9091" {
		t.Fatalf("management listen = %q, want 0.0.0.0:9091", cfg.Management.Listen)
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

func TestNormalizeRuntimeEnums(t *testing.T) {
	if mode, err := NormalizeMode("multi_port"); err != nil || mode != "multi-port" {
		t.Fatalf("NormalizeMode = %q, %v; want multi-port", mode, err)
	}
	if poolMode, err := NormalizePoolMode("BALANCE"); err != nil || poolMode != "balance" {
		t.Fatalf("NormalizePoolMode = %q, %v; want balance", poolMode, err)
	}
	if logLevel, err := NormalizeLogLevel("WARN"); err != nil || logLevel != "warn" {
		t.Fatalf("NormalizeLogLevel = %q, %v; want warn", logLevel, err)
	}
}

func TestNormalizeRuntimeEnumsRejectInvalidValues(t *testing.T) {
	if _, err := NormalizeMode("invalid"); err == nil {
		t.Fatal("expected invalid runtime mode error")
	}
	if _, err := NormalizePoolMode("least_conn"); err == nil {
		t.Fatal("expected invalid pool mode error")
	}
	if _, err := NormalizeLogLevel("verbose"); err == nil {
		t.Fatal("expected invalid log level error")
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

func TestNormalizeWithPortMapPreservesNodePortWithoutSelfConflict(t *testing.T) {
	cfg := Config{
		MultiPort: MultiPortConfig{Address: "127.0.0.1", BasePort: 24000, Protocol: InboundProtocolMixed},
		ProxyPools: []ProxyPoolConfig{{
			Name:    "pool",
			Enabled: true,
			Listener: ListenerConfig{
				Address:  "127.0.0.1",
				Port:     2323,
				Protocol: InboundProtocolMixed,
			},
			Mode:     "sequential",
			AllNodes: true,
		}},
		Nodes: []NodeConfig{{
			Name: "node-1",
			URI:  "http://user:pass@example.com:8080",
		}},
	}
	portMap := map[string]uint16{
		cfg.Nodes[0].NodeKey(): 24000,
	}

	if err := cfg.NormalizeWithPortMap(portMap); err != nil {
		t.Fatalf("normalize with port map: %v", err)
	}
	if cfg.Nodes[0].Port != 24000 {
		t.Fatalf("node port = %d, want 24000", cfg.Nodes[0].Port)
	}
}

func TestNormalizeWithPortMapDefaultsGeoIPPort(t *testing.T) {
	cfg := Config{}
	if err := cfg.NormalizeWithPortMap(nil); err != nil {
		t.Fatalf("normalize config: %v", err)
	}
	if cfg.GeoIP.Port != 1221 {
		t.Fatalf("geoip port = %d, want 1221", cfg.GeoIP.Port)
	}
}

func TestNormalizeWithPortMapRejectsGeoIPProxyPoolPortConflict(t *testing.T) {
	cfg := Config{
		GeoIP: GeoIPConfig{Enabled: true, Port: 2323},
		ProxyPools: []ProxyPoolConfig{{
			Name:    "default",
			Enabled: true,
			Listener: ListenerConfig{
				Address:  "127.0.0.1",
				Port:     2323,
				Protocol: InboundProtocolMixed,
			},
			Mode:     "sequential",
			AllNodes: true,
		}},
	}
	err := cfg.NormalizeWithPortMap(nil)
	if err == nil || !strings.Contains(err.Error(), "geoip port 2323 conflicts") {
		t.Fatalf("normalize error = %v, want geoip port conflict", err)
	}
}

func TestNormalizeWithPortMapRejectsGeoIPNodePortConflict(t *testing.T) {
	cfg := Config{
		GeoIP: GeoIPConfig{Enabled: true, Port: 1221},
		Nodes: []NodeConfig{{
			Name: "node-1",
			URI:  "http://user:pass@example.com:8080",
			Port: 1221,
		}},
	}
	err := cfg.NormalizeWithPortMap(nil)
	if err == nil || !strings.Contains(err.Error(), "node \"node-1\" port 1221 conflicts") {
		t.Fatalf("normalize error = %v, want node port conflict", err)
	}
}
