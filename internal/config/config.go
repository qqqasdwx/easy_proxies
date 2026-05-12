package config

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"easy_proxies/internal/store"

	"gopkg.in/yaml.v3"
)

// Config describes the high level settings for the proxy pool server.
type Config struct {
	Mode                string                    `yaml:"mode"`
	Listener            ListenerConfig            `yaml:"listener"`
	MultiPort           MultiPortConfig           `yaml:"multi_port"`
	Pool                PoolConfig                `yaml:"pool"`
	ProxyPools          []ProxyPoolConfig         `yaml:"proxy_pools" json:"proxy_pools"`
	Management          ManagementConfig          `yaml:"management"`
	SubscriptionRefresh SubscriptionRefreshConfig `yaml:"subscription_refresh"`
	DNS                 DNSConfig                 `yaml:"dns"`
	GeoIP               GeoIPConfig               `yaml:"geoip"`
	HealthCheck         HealthCheckConfig         `yaml:"health_check"`
	Log                 LogConfig                 `yaml:"log"`
	Nodes               []NodeConfig              `yaml:"nodes"`
	DatabasePath        string                    `yaml:"database_path"` // SQLite 数据库路径，默认 data/data.db
	ExternalIP          string                    `yaml:"external_ip"`   // 外部 IP 地址，用于导出时替换 0.0.0.0
	LogLevel            string                    `yaml:"log_level"`
	SkipCertVerify      bool                      `yaml:"skip_cert_verify"` // 全局跳过 SSL 证书验证
}

// DNSConfig controls the optional custom resolver used by sing-box and node metadata lookups.
type DNSConfig struct {
	Enabled         bool     `yaml:"enabled"`
	Server          string   `yaml:"server"`
	FallbackServers []string `yaml:"fallback_servers"`
	Port            uint16   `yaml:"port"`
	Strategy        string   `yaml:"strategy"`
}

// LogConfig controls log output and rotation.
type LogConfig struct {
	Output     string `yaml:"output"`      // 日志输出: "stdout", "file", 默认 "stdout"
	File       string `yaml:"file"`        // 日志文件路径，默认 "logs/easy_proxies.log"
	MaxSize    int    `yaml:"max_size"`    // 单个日志文件最大 MB，默认 50
	MaxBackups int    `yaml:"max_backups"` // 保留旧日志文件个数，默认 3
	MaxAge     int    `yaml:"max_age"`     // 保留旧日志文件天数，默认 7
	Compress   bool   `yaml:"compress"`    // 是否压缩旧日志，默认 false
}

// GeoIPConfig controls GeoIP-based region routing.
type GeoIPConfig struct {
	Enabled            bool          `yaml:"enabled"`              // 是否启用 GeoIP 地域分区
	DatabasePath       string        `yaml:"database_path"`        // 系统管理的 GeoLite2-Country.mmdb 文件路径
	Listen             string        `yaml:"listen"`               // GeoIP 路由监听地址，默认使用 listener 配置
	Port               uint16        `yaml:"port"`                 // GeoIP 路由监听端口，默认 1221
	AutoUpdateEnabled  bool          `yaml:"auto_update_enabled"`  // 是否启用自动更新数据库
	AutoUpdateInterval time.Duration `yaml:"auto_update_interval"` // 自动更新间隔，默认 24 小时
}

// HealthCheckConfig controls periodic and manual node probes.
type HealthCheckConfig struct {
	Interval    time.Duration `yaml:"interval"`
	Timeout     time.Duration `yaml:"timeout"`
	Concurrency int           `yaml:"concurrency"`
}

// ListenerConfig defines how the proxy should listen for clients.
type ListenerConfig struct {
	Address  string `yaml:"address"`
	Port     uint16 `yaml:"port"`
	Protocol string `yaml:"protocol"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// PoolConfig configures scheduling + failure handling.
type PoolConfig struct {
	Mode              string        `yaml:"mode"`
	FailureThreshold  int           `yaml:"failure_threshold"`
	BlacklistDuration time.Duration `yaml:"blacklist_duration"`
}

// ProxyPoolConfig defines an independent local proxy pool entry.
type ProxyPoolConfig struct {
	ID                int64          `yaml:"id" json:"id"`
	Name              string         `yaml:"name" json:"name"`
	Enabled           bool           `yaml:"enabled" json:"enabled"`
	Listener          ListenerConfig `yaml:"listener" json:"listener"`
	Mode              string         `yaml:"mode" json:"mode"`
	FailureThreshold  int            `yaml:"failure_threshold" json:"failure_threshold"`
	BlacklistDuration time.Duration  `yaml:"blacklist_duration" json:"blacklist_duration"`
	AllNodes          bool           `yaml:"all_nodes" json:"all_nodes"`
	NodeIDs           []int64        `yaml:"node_ids" json:"node_ids"`
}

// MultiPortConfig defines address/credential defaults for multi-port mode.
type MultiPortConfig struct {
	Address  string `yaml:"address"`
	BasePort uint16 `yaml:"base_port"`
	Protocol string `yaml:"protocol"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// ManagementConfig controls the monitoring HTTP endpoint.
type ManagementConfig struct {
	Enabled     *bool  `yaml:"enabled"`
	Listen      string `yaml:"listen"`
	ProbeTarget string `yaml:"probe_target"`
	Password    string `yaml:"-"` // WebUI 访问密码，只从环境变量读取，为空则不需要密码
}

// SubscriptionRefreshConfig controls subscription refresh timeouts and reload safety.
type SubscriptionRefreshConfig struct {
	Enabled            bool          `yaml:"enabled"`              // 兼容旧字段：调度以订阅源 auto_update 为准
	Interval           time.Duration `yaml:"interval"`             // 兼容旧字段：订阅源缺省刷新间隔
	Timeout            time.Duration `yaml:"timeout"`              // 获取订阅的超时时间
	HealthCheckTimeout time.Duration `yaml:"health_check_timeout"` // 新节点健康检查超时
	DrainTimeout       time.Duration `yaml:"drain_timeout"`        // 旧实例排空超时时间
	MinAvailableNodes  int           `yaml:"min_available_nodes"`  // 最少可用节点数，低于此值不切换
}

// NodeSource indicates where a node configuration originated from.
type NodeSource string

const (
	NodeSourceSubscription NodeSource = "subscription" // Fetched from subscription URL
	NodeSourceManual       NodeSource = "manual"       // Managed through WebUI/API and persisted in SQLite
)

const (
	InboundProtocolHTTP   = "http"
	InboundProtocolSOCKS5 = "socks5"
	InboundProtocolMixed  = "mixed"
)

const (
	DNSStrategyAsIs       = "as_is"
	DNSStrategyPreferIPv4 = "prefer_ipv4"
	DNSStrategyPreferIPv6 = "prefer_ipv6"
	DNSStrategyIPv4Only   = "ipv4_only"
	DNSStrategyIPv6Only   = "ipv6_only"
)

const (
	EnvManagementPort     = "MANAGEMENT_PORT"
	EnvManagementPassword = "MANAGEMENT_PASSWORD"
)

const runtimeConfigSettingKey = "runtime_config"
const defaultGeoIPDatabaseName = "GeoLite2-Country.mmdb"

// DefaultGeoIPDatabasePath returns the built-in GeoIP database location.
func DefaultGeoIPDatabasePath() string {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return filepath.Join("/app/data", defaultGeoIPDatabaseName)
	}
	return filepath.Join("data", defaultGeoIPDatabaseName)
}

// NormalizeMode normalizes and validates the runtime mode.
func NormalizeMode(value string) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(value))
	if mode == "" {
		return "pool", nil
	}
	if mode == "multi_port" {
		mode = "multi-port"
	}
	switch mode {
	case "pool", "multi-port", "hybrid":
		return mode, nil
	default:
		return "", fmt.Errorf("unsupported mode %q (use 'pool', 'multi-port', or 'hybrid')", value)
	}
}

// NormalizePoolMode normalizes and validates the pool scheduling mode.
func NormalizePoolMode(value string) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(value))
	if mode == "" {
		return "sequential", nil
	}
	switch mode {
	case "sequential", "random", "balance":
		return mode, nil
	default:
		return "", fmt.Errorf("unsupported pool.mode %q (use 'sequential', 'random', or 'balance')", value)
	}
}

// NormalizeLogLevel normalizes and validates the sing-box log level.
func NormalizeLogLevel(value string) (string, error) {
	level := strings.ToLower(strings.TrimSpace(value))
	if level == "" {
		return "info", nil
	}
	switch level {
	case "trace", "debug", "info", "warn", "error", "fatal", "panic":
		return level, nil
	default:
		return "", fmt.Errorf("unsupported log_level %q (use 'trace', 'debug', 'info', 'warn', 'error', 'fatal', or 'panic')", value)
	}
}

// NormalizeInboundProtocol normalizes inbound protocol aliases and validates the value.
func NormalizeInboundProtocol(value string) (string, error) {
	protocol := strings.ToLower(strings.TrimSpace(value))
	if protocol == "" {
		return InboundProtocolMixed, nil
	}
	if protocol == "socks" {
		protocol = InboundProtocolSOCKS5
	}
	switch protocol {
	case InboundProtocolHTTP, InboundProtocolSOCKS5, InboundProtocolMixed:
		return protocol, nil
	default:
		return "", fmt.Errorf("unsupported inbound protocol %q (use 'http', 'socks5', or 'mixed')", value)
	}
}

func (c *Config) normalizeInboundProtocols() error {
	var err error
	c.Listener.Protocol, err = NormalizeInboundProtocol(c.Listener.Protocol)
	if err != nil {
		return fmt.Errorf("listener.protocol: %w", err)
	}
	c.MultiPort.Protocol, err = NormalizeInboundProtocol(c.MultiPort.Protocol)
	if err != nil {
		return fmt.Errorf("multi_port.protocol: %w", err)
	}
	return nil
}

func (c *Config) normalizeDatabasePath() {
	if c.DatabasePath == "" {
		c.DatabasePath = "data/data.db"
	}
}

// NormalizeDNSStrategy normalizes and validates DNS domain strategy values.
func NormalizeDNSStrategy(value string) (string, error) {
	strategy := strings.ToLower(strings.TrimSpace(value))
	if strategy == "" {
		return DNSStrategyPreferIPv4, nil
	}
	switch strategy {
	case DNSStrategyAsIs, DNSStrategyPreferIPv4, DNSStrategyPreferIPv6, DNSStrategyIPv4Only, DNSStrategyIPv6Only:
		return strategy, nil
	default:
		return "", fmt.Errorf("unsupported dns.strategy %q (use 'as_is', 'prefer_ipv4', 'prefer_ipv6', 'ipv4_only', or 'ipv6_only')", value)
	}
}

func (c *Config) normalizeDNSConfig() error {
	var err error
	c.DNS.Strategy, err = NormalizeDNSStrategy(c.DNS.Strategy)
	if err != nil {
		return err
	}
	c.DNS.Server = strings.TrimSpace(c.DNS.Server)
	if c.DNS.Server == "" {
		c.DNS.Server = "223.5.5.5"
	}
	if c.DNS.Port == 0 {
		c.DNS.Port = 53
	}
	if c.DNS.FallbackServers == nil {
		c.DNS.FallbackServers = []string{"8.8.8.8", "1.1.1.1"}
	} else {
		cleaned := c.DNS.FallbackServers[:0]
		for _, server := range c.DNS.FallbackServers {
			server = strings.TrimSpace(server)
			if server != "" {
				cleaned = append(cleaned, server)
			}
		}
		c.DNS.FallbackServers = cleaned
	}
	return nil
}

func (c *Config) normalizeHealthCheckConfig() {
	if c.HealthCheck.Interval <= 0 {
		c.HealthCheck.Interval = 5 * time.Minute
	}
	if c.HealthCheck.Timeout <= 0 {
		c.HealthCheck.Timeout = 10 * time.Second
	}
	if c.HealthCheck.Concurrency <= 0 {
		c.HealthCheck.Concurrency = 8
	}
}

// Default returns the built-in runtime defaults without reading config files.
func Default() (*Config, error) {
	var cfg Config
	if err := cfg.NormalizeWithPortMap(nil); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// RuntimeFromStore loads runtime settings from SQLite and applies defaults plus env overrides.
func RuntimeFromStore(ctx context.Context, st store.Store) (*Config, error) {
	cfg, err := Default()
	if err != nil {
		return nil, err
	}
	if st == nil {
		return cfg, nil
	}
	raw, ok, err := st.GetAppSetting(ctx, runtimeConfigSettingKey)
	if err != nil {
		return nil, err
	}
	if ok && strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), cfg); err != nil {
			return nil, fmt.Errorf("decode runtime config from database: %w", err)
		}
	}
	cfg.Nodes = nil
	proxyPools, err := st.ListProxyPools(ctx)
	if err != nil {
		return nil, fmt.Errorf("load proxy pools: %w", err)
	}
	cfg.ProxyPools = proxyPoolsFromStore(proxyPools)
	if err := cfg.NormalizeWithPortMap(nil); err != nil {
		return nil, err
	}
	return cfg, nil
}

// SaveRuntime persists runtime settings to SQLite. Management.Password is intentionally omitted.
func SaveRuntime(ctx context.Context, st store.Store, cfg *Config) error {
	if st == nil {
		return errors.New("store is nil")
	}
	if cfg == nil {
		return errors.New("config is nil")
	}
	saveCfg := *cfg
	saveCfg.Nodes = nil
	saveCfg.ProxyPools = nil
	managementEnabled := true
	saveCfg.Management.Enabled = &managementEnabled
	saveCfg.Management.Listen = "0.0.0.0:9091"
	saveCfg.Management.Password = ""
	data, err := json.Marshal(&saveCfg)
	if err != nil {
		return fmt.Errorf("encode runtime config: %w", err)
	}
	return st.SetAppSetting(ctx, runtimeConfigSettingKey, string(data))
}

// NodeConfig describes a single upstream proxy endpoint expressed as URI.
type NodeConfig struct {
	ID              int64      `yaml:"-" json:"id,omitempty"`
	Name            string     `yaml:"name" json:"name"`
	URI             string     `yaml:"uri" json:"uri"`
	OutboundJSON    string     `yaml:"outbound_json,omitempty" json:"outbound_json,omitempty"`
	Port            uint16     `yaml:"port,omitempty" json:"port,omitempty"`
	InboundProtocol string     `yaml:"inbound_protocol,omitempty" json:"inbound_protocol,omitempty"`
	Username        string     `yaml:"username,omitempty" json:"username,omitempty"`
	Password        string     `yaml:"password,omitempty" json:"password,omitempty"`
	Source          NodeSource `yaml:"-" json:"source,omitempty"` // Runtime only, not persisted
	Disabled        bool       `yaml:"-" json:"disabled,omitempty"`
}

// NodeKey returns a stable identifier for preserving runtime state.
// This is used to preserve port assignments across reloads.
func (n *NodeConfig) NodeKey() string {
	if n.URI != "" {
		return n.URI
	}
	sum := sha256.Sum256([]byte(n.OutboundJSON))
	return "outbound-json:" + hex.EncodeToString(sum[:])
}

// ExtractNodeName extracts a human-readable name from a proxy URI.
// For standard URIs (vless://, ss://, trojan://), it extracts from the URL fragment (#name).
// For vmess:// URIs, it base64-decodes the payload and extracts the "ps" field.
func ExtractNodeName(uri string) string {
	uri = strings.TrimSpace(uri)

	// Handle vmess:// specially - it's base64-encoded JSON, not a standard URL
	if strings.HasPrefix(uri, "vmess://") {
		payload := strings.TrimPrefix(uri, "vmess://")
		// Remove any fragment that might be appended
		if idx := strings.Index(payload, "#"); idx != -1 {
			payload = payload[:idx]
		}
		payload = strings.TrimSpace(payload)
		// Try standard base64 first, then raw/URL-safe variants
		var decoded []byte
		var err error
		decoded, err = base64.StdEncoding.DecodeString(payload)
		if err != nil {
			decoded, err = base64.RawStdEncoding.DecodeString(payload)
		}
		if err != nil {
			decoded, err = base64.RawURLEncoding.DecodeString(payload)
		}
		if err == nil {
			var vmess struct {
				PS string `json:"ps"`
			}
			if json.Unmarshal(decoded, &vmess) == nil && vmess.PS != "" {
				return strings.TrimSpace(vmess.PS)
			}
		}
		return ""
	}

	// For standard URIs, extract from URL fragment (#name)
	if idx := strings.LastIndex(uri, "#"); idx != -1 && idx < len(uri)-1 {
		fragment := uri[idx+1:]
		if decoded, err := url.QueryUnescape(fragment); err == nil && decoded != "" {
			return strings.TrimSpace(decoded)
		}
		return strings.TrimSpace(fragment)
	}

	return ""
}

// BuildPortMap creates a mapping from node URI to port for existing nodes.
// This is used to preserve port assignments when reloading configuration.
func (c *Config) BuildPortMap() map[string]uint16 {
	portMap := make(map[string]uint16)
	for _, node := range c.Nodes {
		if node.Port > 0 {
			portMap[node.NodeKey()] = node.Port
		}
	}
	return portMap
}

// NormalizeWithPortMap applies defaults and validation, preserving port assignments
// for nodes that exist in the provided port map.
func (c *Config) NormalizeWithPortMap(portMap map[string]uint16) error {
	var err error
	c.Mode, err = NormalizeMode(c.Mode)
	if err != nil {
		return err
	}
	if c.Listener.Address == "" {
		c.Listener.Address = "0.0.0.0"
	}
	if c.Listener.Port == 0 {
		c.Listener.Port = 2323
	}
	c.Pool.Mode, err = NormalizePoolMode(c.Pool.Mode)
	if err != nil {
		return err
	}
	if c.Pool.FailureThreshold <= 0 {
		c.Pool.FailureThreshold = 3
	}
	if c.Pool.BlacklistDuration <= 0 {
		c.Pool.BlacklistDuration = 24 * time.Hour
	}
	if c.MultiPort.Address == "" {
		c.MultiPort.Address = "0.0.0.0"
	}
	if c.MultiPort.BasePort == 0 {
		c.MultiPort.BasePort = 24000
	}
	if err := c.normalizeInboundProtocols(); err != nil {
		return err
	}
	if err := c.normalizeProxyPools(); err != nil {
		return err
	}
	if err := c.normalizeDNSConfig(); err != nil {
		return err
	}
	c.Management.Listen = "0.0.0.0:9091"
	if c.Management.ProbeTarget == "" {
		c.Management.ProbeTarget = "www.apple.com:80"
	}
	managementEnabled := true
	c.Management.Enabled = &managementEnabled
	c.normalizeDatabasePath()
	c.GeoIP.DatabasePath = DefaultGeoIPDatabasePath()
	if c.GeoIP.AutoUpdateInterval <= 0 {
		c.GeoIP.AutoUpdateInterval = 24 * time.Hour
	}
	if c.SubscriptionRefresh.Interval <= 0 {
		c.SubscriptionRefresh.Interval = 1 * time.Hour
	}
	if c.SubscriptionRefresh.Timeout <= 0 {
		c.SubscriptionRefresh.Timeout = 30 * time.Second
	}
	if c.SubscriptionRefresh.HealthCheckTimeout <= 0 {
		c.SubscriptionRefresh.HealthCheckTimeout = 60 * time.Second
	}
	if c.SubscriptionRefresh.DrainTimeout <= 0 {
		c.SubscriptionRefresh.DrainTimeout = 30 * time.Second
	}
	if c.SubscriptionRefresh.MinAvailableNodes <= 0 {
		c.SubscriptionRefresh.MinAvailableNodes = 1
	}
	c.normalizeHealthCheckConfig()

	// Build set of ports already assigned from portMap
	usedPorts := make(map[uint16]bool)
	for _, pool := range c.ProxyPools {
		if pool.Enabled && pool.Listener.Port > 0 {
			usedPorts[pool.Listener.Port] = true
		}
	}

	// First pass: assign ports from portMap for existing nodes
	for idx := range c.Nodes {
		c.Nodes[idx].Name = strings.TrimSpace(c.Nodes[idx].Name)
		c.Nodes[idx].URI = strings.TrimSpace(c.Nodes[idx].URI)
		c.Nodes[idx].OutboundJSON = strings.TrimSpace(c.Nodes[idx].OutboundJSON)
		if c.Nodes[idx].URI == "" && c.Nodes[idx].OutboundJSON == "" {
			return fmt.Errorf("node %d is missing uri or outbound_json", idx)
		}
		c.Nodes[idx].InboundProtocol = strings.TrimSpace(c.Nodes[idx].InboundProtocol)
		if c.Nodes[idx].InboundProtocol != "" {
			if protocol, err := NormalizeInboundProtocol(c.Nodes[idx].InboundProtocol); err != nil {
				return fmt.Errorf("node %d inbound_protocol: %w", idx, err)
			} else {
				c.Nodes[idx].InboundProtocol = protocol
			}
		}

		// Auto-extract name from URI if not provided
		if c.Nodes[idx].Name == "" {
			c.Nodes[idx].Name = ExtractNodeName(c.Nodes[idx].URI)
		}
		if c.Nodes[idx].Name == "" {
			c.Nodes[idx].Name = fmt.Sprintf("node-%d", idx)
		}

		// Check if this node has a preserved port from portMap
		nodeKey := c.Nodes[idx].NodeKey()
		if existingPort, ok := portMap[nodeKey]; ok && existingPort > 0 {
			c.Nodes[idx].Port = existingPort
			log.Printf("✅ Preserved port %d for node %q", existingPort, c.Nodes[idx].Name)
		}
		if c.Nodes[idx].Port > 0 {
			if usedPorts[c.Nodes[idx].Port] {
				return fmt.Errorf("node %q port %d conflicts with another listener", c.Nodes[idx].Name, c.Nodes[idx].Port)
			}
			usedPorts[c.Nodes[idx].Port] = true
		}
	}

	// Second pass: keep node local listeners opt-in. A zero port means the node
	// does not expose a dedicated local inbound.
	for idx := range c.Nodes {
		if c.Nodes[idx].Port > 0 {
			if c.Nodes[idx].Username == "" {
				c.Nodes[idx].Username = c.MultiPort.Username
				c.Nodes[idx].Password = c.MultiPort.Password
			}
		}
	}

	c.LogLevel, err = NormalizeLogLevel(c.LogLevel)
	if err != nil {
		return err
	}

	c.normalizeLogConfig()

	if err := c.applyEnvironmentOverrides(); err != nil {
		return err
	}

	return nil
}

func (c *Config) normalizeProxyPools() error {
	if len(c.ProxyPools) == 0 {
		c.ProxyPools = []ProxyPoolConfig{DefaultProxyPoolConfig()}
	}
	usedPorts := make(map[uint16]string)
	for idx := range c.ProxyPools {
		pool := &c.ProxyPools[idx]
		pool.Name = strings.TrimSpace(pool.Name)
		if pool.Name == "" {
			pool.Name = fmt.Sprintf("代理池 %d", idx+1)
		}
		if pool.Listener.Address == "" {
			pool.Listener.Address = "0.0.0.0"
		}
		if pool.Listener.Port == 0 {
			pool.Listener.Port = uint16(2323 + idx)
		}
		protocol, err := NormalizeInboundProtocol(pool.Listener.Protocol)
		if err != nil {
			return fmt.Errorf("proxy_pools[%d].listener.protocol: %w", idx, err)
		}
		pool.Listener.Protocol = protocol
		mode, err := NormalizePoolMode(pool.Mode)
		if err != nil {
			return fmt.Errorf("proxy_pools[%d].mode: %w", idx, err)
		}
		pool.Mode = mode
		if pool.FailureThreshold <= 0 {
			pool.FailureThreshold = 3
		}
		if pool.BlacklistDuration <= 0 {
			pool.BlacklistDuration = 24 * time.Hour
		}
		if pool.Enabled && pool.Listener.Port > 0 {
			if owner := usedPorts[pool.Listener.Port]; owner != "" {
				return fmt.Errorf("proxy pool %q port %d conflicts with %q", pool.Name, pool.Listener.Port, owner)
			}
			usedPorts[pool.Listener.Port] = pool.Name
		}
	}
	return nil
}

// DefaultProxyPoolConfig returns the first-run local proxy pool.
func DefaultProxyPoolConfig() ProxyPoolConfig {
	return ProxyPoolConfig{
		ID:      1,
		Name:    "默认代理池",
		Enabled: true,
		Listener: ListenerConfig{
			Address:  "0.0.0.0",
			Port:     2323,
			Protocol: InboundProtocolMixed,
		},
		Mode:              "sequential",
		FailureThreshold:  3,
		BlacklistDuration: 24 * time.Hour,
		AllNodes:          true,
	}
}

func proxyPoolsFromStore(pools []store.ProxyPool) []ProxyPoolConfig {
	if len(pools) == 0 {
		return nil
	}
	out := make([]ProxyPoolConfig, 0, len(pools))
	for _, pool := range pools {
		out = append(out, ProxyPoolConfig{
			ID:      pool.ID,
			Name:    pool.Name,
			Enabled: pool.Enabled,
			Listener: ListenerConfig{
				Address:  pool.ListenAddress,
				Port:     pool.ListenPort,
				Protocol: pool.Protocol,
				Username: pool.Username,
				Password: pool.Password,
			},
			Mode:              pool.Mode,
			FailureThreshold:  pool.FailureThreshold,
			BlacklistDuration: pool.BlacklistDuration,
			AllNodes:          pool.AllNodes,
			NodeIDs:           append([]int64(nil), pool.NodeIDs...),
		})
	}
	return out
}

// normalizeLogConfig applies defaults to the log config.
func (c *Config) normalizeLogConfig() {
	if c.Log.Output == "" {
		c.Log.Output = "stdout"
	}
	if c.Log.File == "" {
		c.Log.File = "logs/easy_proxies.log"
	}
	if c.Log.MaxSize <= 0 {
		c.Log.MaxSize = 50
	}
	if c.Log.MaxBackups <= 0 {
		c.Log.MaxBackups = 3
	}
	if c.Log.MaxAge <= 0 {
		c.Log.MaxAge = 7
	}
}

func (c *Config) applyEnvironmentOverrides() error {
	if portValue, ok := firstEnv(EnvManagementPort); ok {
		port, err := strconv.ParseUint(strings.TrimSpace(portValue), 10, 16)
		if err != nil || port == 0 {
			return fmt.Errorf("%s must be a TCP port between 1 and 65535", EnvManagementPort)
		}
		c.Management.Listen = replaceListenPort(c.Management.Listen, uint16(port))
	}
	if password, ok := firstEnv(EnvManagementPassword); ok {
		c.Management.Password = password
	} else {
		c.Management.Password = ""
	}
	return nil
}

func firstEnv(names ...string) (string, bool) {
	for _, name := range names {
		value, ok := os.LookupEnv(name)
		if ok {
			return value, true
		}
	}
	return "", false
}

func replaceListenPort(listen string, port uint16) string {
	host := "0.0.0.0"
	if listen != "" {
		if h, _, err := net.SplitHostPort(listen); err == nil {
			host = h
		}
	}
	return net.JoinHostPort(host, strconv.Itoa(int(port)))
}

// ManagementEnabled reports whether the monitoring endpoint should run.
// The management server is intentionally always enabled; access is controlled
// by MANAGEMENT_PORT and MANAGEMENT_PASSWORD at process/container startup.
func (c *Config) ManagementEnabled() bool {
	return true
}

// parseSubscriptionContent tries to parse subscription content in various formats (optimized)
func parseSubscriptionContent(content string) ([]NodeConfig, error) {
	content = strings.TrimSpace(content)

	// Quick check for YAML format (check first 16384 chars for "proxies:")
	sampleSize := 16384
	if len(content) < sampleSize {
		sampleSize = len(content)
	}
	if strings.Contains(content[:sampleSize], "proxies:") {
		return parseClashYAML(content)
	}

	// Check if it's base64 encoded (common for v2ray subscriptions)
	if isBase64(content) {
		decoded, err := base64.StdEncoding.DecodeString(content)
		if err != nil {
			// Try URL-safe base64
			decoded, err = base64.RawStdEncoding.DecodeString(content)
			if err != nil {
				// Not base64, try as plain text
				return parseNodesFromContent(content)
			}
		}
		content = string(decoded)
	}

	// Parse as plain text (one URI per line)
	return parseNodesFromContent(content)
}

// ParseSubscriptionContent parses subscription content in various formats (base64, plain text, Clash YAML).
// This is exported for use by the subscription manager.
func ParseSubscriptionContent(content string) ([]NodeConfig, error) {
	return parseSubscriptionContent(content)
}

// parseNodesFromContent parses nodes from plain text content (one URI per line)
func parseNodesFromContent(content string) ([]NodeConfig, error) {
	var nodes []NodeConfig
	lines := strings.Split(content, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Check if it's a valid proxy URI
		if IsProxyURI(line) {
			nodes = append(nodes, NodeConfig{
				URI: line,
			})
		}
	}

	return nodes, nil
}

// isBase64 checks if a string looks like base64 encoded content (optimized version)
func isBase64(s string) bool {
	// Remove whitespace
	s = strings.TrimSpace(s)
	if len(s) == 0 {
		return false
	}

	// Remove newlines for checking
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")

	// Quick check: if it contains proxy URI schemes, it's not base64
	if strings.Contains(s, "://") {
		return false
	}

	// Check character set - base64 only contains A-Za-z0-9+/=
	// This is much faster than trying to decode
	for _, c := range s {
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
			(c >= '0' && c <= '9') || c == '+' || c == '/' || c == '=') {
			return false
		}
	}

	// Length must be multiple of 4 (with padding)
	return len(s)%4 == 0
}

// IsProxyURI checks if a string is a valid proxy URI
func IsProxyURI(s string) bool {
	schemes := []string{"vmess://", "vless://", "trojan://", "ss://", "ssr://", "hysteria://", "hysteria2://", "hy2://", "tuic://", "socks5://", "socks://", "http://", "https://", "anytls://"}
	lower := strings.ToLower(s)
	for _, scheme := range schemes {
		if strings.HasPrefix(lower, scheme) {
			return true
		}
	}
	return false
}

// clashConfig represents a minimal Clash configuration for parsing proxies
// flexInt handles YAML values that may be either int or quoted string.
type flexInt int

func (fi *flexInt) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var intVal int
	if err := unmarshal(&intVal); err == nil {
		*fi = flexInt(intVal)
		return nil
	}
	var strVal string
	if err := unmarshal(&strVal); err != nil {
		return fmt.Errorf("cannot unmarshal port: expected int or string")
	}
	parsed, err := strconv.Atoi(strVal)
	if err != nil {
		return fmt.Errorf("cannot parse port %q as int: %w", strVal, err)
	}
	*fi = flexInt(parsed)
	return nil
}

type clashConfig struct {
	Proxies []clashProxy `yaml:"proxies"`
}

type clashProxy struct {
	Name              string                 `yaml:"name"`
	Type              string                 `yaml:"type"`
	Server            string                 `yaml:"server"`
	Port              flexInt                `yaml:"port"`
	Ports             string                 `yaml:"ports"`
	UUID              string                 `yaml:"uuid"`
	Password          string                 `yaml:"password"`
	Cipher            string                 `yaml:"cipher"`
	AlterId           int                    `yaml:"alterId"`
	Network           string                 `yaml:"network"`
	TLS               bool                   `yaml:"tls"`
	SkipCertVerify    bool                   `yaml:"skip-cert-verify"`
	ServerName        string                 `yaml:"servername"`
	SNI               string                 `yaml:"sni"`
	Flow              string                 `yaml:"flow"`
	UDP               bool                   `yaml:"udp"`
	WSOpts            *clashWSOptions        `yaml:"ws-opts"`
	GrpcOpts          *clashGrpcOptions      `yaml:"grpc-opts"`
	RealityOpts       *clashRealityOptions   `yaml:"reality-opts"`
	ClientFingerprint string                 `yaml:"client-fingerprint"`
	Obfs              string                 `yaml:"obfs"`
	ObfsPassword      string                 `yaml:"obfs-password"`
	Plugin            string                 `yaml:"plugin"`
	PluginOpts        map[string]interface{} `yaml:"plugin-opts"`
	// TUIC-specific fields
	ALPN                 []string `yaml:"alpn"`
	CongestionController string   `yaml:"congestion-controller"`
	UDPRelayMode         string   `yaml:"udp-relay-mode"`
}

type clashWSOptions struct {
	Path    string            `yaml:"path"`
	Headers map[string]string `yaml:"headers"`
}

type clashGrpcOptions struct {
	GrpcServiceName string `yaml:"grpc-service-name"`
}

type clashRealityOptions struct {
	PublicKey string `yaml:"public-key"`
	ShortID   string `yaml:"short-id"`
}

// parseClashYAML parses Clash YAML format and converts to NodeConfig
func parseClashYAML(content string) ([]NodeConfig, error) {
	var clash clashConfig
	if err := yaml.Unmarshal([]byte(content), &clash); err != nil {
		return nil, fmt.Errorf("parse clash yaml: %w", err)
	}

	var nodes []NodeConfig
	for _, proxy := range clash.Proxies {
		uri := convertClashProxyToURI(proxy)
		if uri != "" {
			nodes = append(nodes, NodeConfig{
				Name: proxy.Name,
				URI:  uri,
			})
		}
	}

	return nodes, nil
}

// convertClashProxyToURI converts a Clash proxy config to a standard URI
func convertClashProxyToURI(p clashProxy) string {
	switch strings.ToLower(p.Type) {
	case "vmess":
		return buildVMessURI(p)
	case "vless":
		return buildVLESSURI(p)
	case "trojan":
		return buildTrojanURI(p)
	case "anytls":
		return buildAnyTLSURI(p)
	case "ss", "shadowsocks":
		return buildShadowsocksURI(p)
	case "hysteria2", "hy2":
		return buildHysteria2URI(p)
	case "tuic":
		return buildTUICURI(p)
	default:
		return ""
	}
}

func buildVMessURI(p clashProxy) string {
	params := url.Values{}
	if p.Network != "" && p.Network != "tcp" {
		params.Set("type", p.Network)
	}
	if p.TLS {
		params.Set("security", "tls")
		if p.ServerName != "" {
			params.Set("sni", p.ServerName)
		} else if p.SNI != "" {
			params.Set("sni", p.SNI)
		}
	}
	if p.WSOpts != nil {
		if p.WSOpts.Path != "" {
			params.Set("path", p.WSOpts.Path)
		}
		if host, ok := p.WSOpts.Headers["Host"]; ok {
			params.Set("host", host)
		}
	}
	if p.ClientFingerprint != "" {
		params.Set("fp", p.ClientFingerprint)
	}

	query := ""
	if len(params) > 0 {
		query = "?" + params.Encode()
	}

	return fmt.Sprintf("vmess://%s@%s:%d%s#%s", p.UUID, p.Server, int(p.Port), query, url.QueryEscape(p.Name))
}

func buildVLESSURI(p clashProxy) string {
	params := url.Values{}
	params.Set("encryption", "none")

	if p.Network != "" && p.Network != "tcp" {
		params.Set("type", p.Network)
	}
	if p.Flow != "" {
		params.Set("flow", p.Flow)
	}
	if p.TLS {
		params.Set("security", "tls")
		if p.ServerName != "" {
			params.Set("sni", p.ServerName)
		} else if p.SNI != "" {
			params.Set("sni", p.SNI)
		}
	}
	if p.RealityOpts != nil {
		params.Set("security", "reality")
		if p.RealityOpts.PublicKey != "" {
			params.Set("pbk", p.RealityOpts.PublicKey)
		}
		if p.RealityOpts.ShortID != "" {
			params.Set("sid", p.RealityOpts.ShortID)
		}
		if p.ServerName != "" {
			params.Set("sni", p.ServerName)
		}
	}
	if p.WSOpts != nil {
		if p.WSOpts.Path != "" {
			params.Set("path", p.WSOpts.Path)
		}
		if host, ok := p.WSOpts.Headers["Host"]; ok {
			params.Set("host", host)
		}
	}
	if p.GrpcOpts != nil && p.GrpcOpts.GrpcServiceName != "" {
		params.Set("serviceName", p.GrpcOpts.GrpcServiceName)
	}
	if p.ClientFingerprint != "" {
		params.Set("fp", p.ClientFingerprint)
	}

	return fmt.Sprintf("vless://%s@%s:%d?%s#%s", p.UUID, p.Server, int(p.Port), params.Encode(), url.QueryEscape(p.Name))
}

func buildTrojanURI(p clashProxy) string {
	params := url.Values{}
	if p.ServerName != "" {
		params.Set("sni", p.ServerName)
	} else if p.SNI != "" {
		params.Set("sni", p.SNI)
	}
	if p.SkipCertVerify {
		params.Set("allowInsecure", "1")
	}
	if p.Network != "" && p.Network != "tcp" {
		params.Set("type", p.Network)
	}
	if p.WSOpts != nil {
		if p.WSOpts.Path != "" {
			params.Set("path", p.WSOpts.Path)
		}
		if host, ok := p.WSOpts.Headers["Host"]; ok {
			params.Set("host", host)
		}
	}
	if p.ClientFingerprint != "" {
		params.Set("fp", p.ClientFingerprint)
	}

	query := ""
	if len(params) > 0 {
		query = "?" + params.Encode()
	}

	return fmt.Sprintf("trojan://%s@%s:%d%s#%s", p.Password, p.Server, int(p.Port), query, url.QueryEscape(p.Name))
}

func buildAnyTLSURI(p clashProxy) string {
	params := url.Values{}
	if p.ServerName != "" {
		params.Set("sni", p.ServerName)
	} else if p.SNI != "" {
		params.Set("sni", p.SNI)
	}
	if p.SkipCertVerify {
		params.Set("allowInsecure", "1")
	}
	if p.ClientFingerprint != "" {
		params.Set("fp", p.ClientFingerprint)
	}

	query := ""
	if len(params) > 0 {
		query = "?" + params.Encode()
	}

	return fmt.Sprintf("anytls://%s@%s:%d%s#%s", p.Password, p.Server, int(p.Port), query, url.QueryEscape(p.Name))
}

func buildShadowsocksURI(p clashProxy) string {
	// Encode method:password in base64
	userInfo := base64.StdEncoding.EncodeToString([]byte(p.Cipher + ":" + p.Password))
	return fmt.Sprintf("ss://%s@%s:%d#%s", userInfo, p.Server, int(p.Port), url.QueryEscape(p.Name))
}

func buildHysteria2URI(p clashProxy) string {
	params := url.Values{}
	if p.ServerName != "" {
		params.Set("sni", p.ServerName)
	} else if p.SNI != "" {
		params.Set("sni", p.SNI)
	}
	if p.SkipCertVerify {
		params.Set("insecure", "1")
	}
	if p.Obfs != "" {
		params.Set("obfs", p.Obfs)
		if p.ObfsPassword != "" {
			params.Set("obfs-password", p.ObfsPassword)
		}
	}
	if strings.TrimSpace(p.Ports) != "" {
		params.Set("ports", normalizeHysteria2PortsValue(strings.TrimSpace(p.Ports)))
	}

	query := ""
	if len(params) > 0 {
		query = "?" + params.Encode()
	}

	port := int(p.Port)
	if port <= 0 {
		port = 443
	}

	return fmt.Sprintf("hysteria2://%s@%s:%d%s#%s", p.Password, p.Server, port, query, url.QueryEscape(p.Name))
}

func normalizeHysteria2PortsValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}

	parts := strings.Split(value, ",")
	normalized := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.Contains(part, ":") {
			normalized = append(normalized, part)
			continue
		}
		if strings.Count(part, "-") == 1 {
			normalized = append(normalized, strings.Replace(part, "-", ":", 1))
			continue
		}
		normalized = append(normalized, part)
	}

	return strings.Join(normalized, ",")
}

func buildTUICURI(p clashProxy) string {
	params := url.Values{}
	if p.ServerName != "" {
		params.Set("sni", p.ServerName)
	} else if p.SNI != "" {
		params.Set("sni", p.SNI)
	}
	if p.SkipCertVerify {
		params.Set("allowInsecure", "1")
	}
	if p.CongestionController != "" {
		params.Set("congestion_control", p.CongestionController)
	}
	if p.UDPRelayMode != "" {
		params.Set("udp_relay_mode", p.UDPRelayMode)
	}
	if len(p.ALPN) > 0 {
		params.Set("alpn", strings.Join(p.ALPN, ","))
	}

	query := ""
	if len(params) > 0 {
		query = "?" + params.Encode()
	}

	// TUIC URI format: tuic://uuid:password@server:port?params#name
	return fmt.Sprintf("tuic://%s:%s@%s:%d%s#%s", p.UUID, p.Password, p.Server, int(p.Port), query, url.QueryEscape(p.Name))
}

// IsPortAvailable checks if a port is available for binding.
func IsPortAvailable(address string, port uint16) bool {
	addr := fmt.Sprintf("%s:%d", address, port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}
