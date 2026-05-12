package builder

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"easy_proxies/internal/config"
	"easy_proxies/internal/geoip"
	poolout "easy_proxies/internal/outbound/pool"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/auth"
	boxjson "github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/common/json/badoption"
)

// Build converts high level config into sing-box Options tree.
func Build(cfg *config.Config) (option.Options, error) {
	proxyPools := effectiveProxyPools(cfg)
	baseOutbounds := make([]option.Outbound, 0, len(cfg.Nodes))
	memberTags := make([]string, 0, len(cfg.Nodes))
	metadata := make(map[string]poolout.MemberMeta)
	nodesByTag := make(map[string]config.NodeConfig)
	nodeIDToTag := make(map[int64]string)
	var failedNodes []string
	usedTags := make(map[string]int) // Track tag usage for uniqueness

	// Initialize GeoIP lookup if enabled
	var geoLookup *geoip.Lookup
	if cfg.GeoIP.Enabled && cfg.GeoIP.DatabasePath != "" {
		var err error
		resolverConfig := geoIPResolverConfig(cfg.DNS)
		// Use auto-update if enabled
		if cfg.GeoIP.AutoUpdateEnabled {
			interval := cfg.GeoIP.AutoUpdateInterval
			if interval == 0 {
				interval = 24 * time.Hour // Default to 24 hours
			}
			geoLookup, err = geoip.NewWithAutoUpdateAndResolver(cfg.GeoIP.DatabasePath, interval, resolverConfig)
		} else {
			geoLookup, err = geoip.NewWithResolver(cfg.GeoIP.DatabasePath, resolverConfig)
		}
		if err != nil {
			log.Printf("⚠️  GeoIP database load failed: %v (region routing disabled)", err)
		} else {
			log.Printf("✅ GeoIP database loaded: %s", cfg.GeoIP.DatabasePath)
		}
	}

	// Track nodes by region for GeoIP routing
	regionMembers := make(map[string][]string)
	for _, region := range geoip.AllRegions() {
		regionMembers[region] = []string{}
	}

	activeNodes := make([]config.NodeConfig, 0, len(cfg.Nodes))
	for _, node := range cfg.Nodes {
		if !node.Disabled {
			activeNodes = append(activeNodes, node)
		}
	}

	totalNodes := len(activeNodes)
	for i, node := range activeNodes {
		if i > 0 && i%1000 == 0 {
			log.Printf("⏳ Building nodes... %d/%d", i, totalNodes)
		}
		baseTag := sanitizeTag(node.Name)
		if baseTag == "" {
			baseTag = fmt.Sprintf("node-%d", len(memberTags)+1)
		}

		// Ensure tag uniqueness by appending a counter if needed
		tag := baseTag
		if count, exists := usedTags[baseTag]; exists {
			usedTags[baseTag] = count + 1
			tag = fmt.Sprintf("%s-%d", baseTag, count+1)
		} else {
			usedTags[baseTag] = 1
		}

		outbound, err := buildNodeConfigOutbound(tag, node, cfg.SkipCertVerify)
		if err != nil {
			log.Printf("❌ Failed to build node '%s': %v (skipping)", node.Name, err)
			failedNodes = append(failedNodes, node.Name)
			continue
		}
		memberTags = append(memberTags, tag)
		baseOutbounds = append(baseOutbounds, outbound)
		nodesByTag[tag] = node
		if node.ID > 0 {
			nodeIDToTag[node.ID] = tag
		}
		meta := poolout.MemberMeta{
			Name: node.Name,
			URI:  node.URI,
			Mode: "node",
		}
		if node.Port > 0 {
			meta.Mode = "multi-port"
			meta.ListenAddress = cfg.MultiPort.Address
			meta.Port = node.Port
		}

		// Default region (will be updated by concurrent GeoIP resolution)
		meta.Region = geoip.RegionOther
		meta.Country = "Unknown"

		metadata[tag] = meta
	}

	// Concurrent GeoIP resolution
	if geoLookup != nil && geoLookup.IsEnabled() {
		geoStart := time.Now()
		log.Printf("🌍 Resolving GeoIP for %d nodes (concurrent)...", len(memberTags))

		type geoResult struct {
			index  int
			tag    string
			region geoip.RegionInfo
		}

		results := make(chan geoResult, len(memberTags))
		var wg sync.WaitGroup

		// Worker pool: min(32, len(memberTags))
		workerCount := 32
		if len(memberTags) < workerCount {
			workerCount = len(memberTags)
		}

		// Job channel
		type geoJob struct {
			index int
			tag   string
			uri   string
		}
		jobs := make(chan geoJob, len(memberTags))

		// Start workers
		for w := 0; w < workerCount; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for job := range jobs {
					region := geoLookup.LookupURI(job.uri)
					results <- geoResult{index: job.index, tag: job.tag, region: region}
				}
			}()
		}

		// Send jobs
		for i, tag := range memberTags {
			meta := metadata[tag]
			jobs <- geoJob{index: i, tag: tag, uri: meta.URI}
		}
		close(jobs)

		// Wait for completion and close results
		go func() {
			wg.Wait()
			close(results)
		}()

		// Collect results
		for res := range results {
			meta := metadata[res.tag]
			meta.Region = res.region.Code
			meta.Country = res.region.Country
			metadata[res.tag] = meta
			regionMembers[res.region.Code] = append(regionMembers[res.region.Code], res.tag)
		}

		log.Printf("🌍 GeoIP resolution completed in %.1fs", time.Since(geoStart).Seconds())
	} else {
		// No GeoIP - assign all to "other" region
		for _, tag := range memberTags {
			regionMembers[geoip.RegionOther] = append(regionMembers[geoip.RegionOther], tag)
		}
	}

	// Close GeoIP database after lookup
	if geoLookup != nil {
		geoLookup.Close()
	}

	// Check if we have at least one valid node
	if len(baseOutbounds) == 0 {
		return option.Options{}, fmt.Errorf("no valid nodes available (all %d nodes failed to build)", len(cfg.Nodes))
	}

	// Log summary
	if len(failedNodes) > 0 {
		log.Printf("⚠️  %d/%d nodes failed and were skipped: %v", len(failedNodes), len(cfg.Nodes), failedNodes)
	}
	log.Printf("✅ Successfully built %d/%d nodes", len(baseOutbounds), len(cfg.Nodes))

	// Log GeoIP region distribution
	if cfg.GeoIP.Enabled {
		log.Println("🌍 GeoIP Region Distribution:")
		for _, region := range geoip.AllRegions() {
			count := len(regionMembers[region])
			if count > 0 {
				log.Printf("   %s %s: %d nodes", geoip.RegionEmoji(region), geoip.RegionName(region), count)
			}
		}
	}

	// Print proxy links for each node
	printProxyLinks(cfg, metadata)

	var (
		inbounds  []option.Inbound
		outbounds = make([]option.Outbound, len(baseOutbounds))
		route     option.RouteOptions
	)
	copy(outbounds, baseOutbounds)

	firstPoolTag := ""
	for _, proxyPool := range proxyPools {
		if !proxyPool.Enabled {
			continue
		}
		poolMembers := proxyPoolMembers(proxyPool, memberTags, nodeIDToTag)
		if len(poolMembers) == 0 {
			log.Printf("⚠️  Proxy pool %q has no active members (skipping)", proxyPool.Name)
			continue
		}
		inboundTag := proxyPoolInboundTag(proxyPool)
		poolTag := proxyPoolTag(proxyPool)
		inbound, err := buildInboundFromListener(proxyPool.Listener, inboundTag)
		if err != nil {
			return option.Options{}, fmt.Errorf("build proxy pool inbound %q: %w", proxyPool.Name, err)
		}
		inbounds = append(inbounds, inbound)
		poolOptions := poolout.Options{
			Mode:              proxyPool.Mode,
			Members:           poolMembers,
			FailureThreshold:  proxyPool.FailureThreshold,
			BlacklistDuration: proxyPool.BlacklistDuration,
			Metadata:          metadataForMembers(metadata, poolMembers),
		}
		outbounds = append(outbounds, option.Outbound{
			Type:    poolout.Type,
			Tag:     poolTag,
			Options: &poolOptions,
		})
		if firstPoolTag == "" {
			firstPoolTag = poolTag
			route.Final = poolTag
		}
		route.Rules = append(route.Rules, option.Rule{
			Type: C.RuleTypeDefault,
			DefaultOptions: option.DefaultRule{
				RawDefaultRule: option.RawDefaultRule{
					Inbound: badoption.Listable[string]{inboundTag},
				},
				RuleAction: option.RuleAction{
					Action: C.RuleActionTypeRoute,
					RouteOptions: option.RouteActionOptions{
						Outbound: poolTag,
					},
				},
			},
		})
	}

	// Build multi-port inbounds (one port per node) for nodes that opt in.
	if hasNodeLocalListeners(activeNodes) {
		addr, err := parseAddr(cfg.MultiPort.Address)
		if err != nil {
			return option.Options{}, fmt.Errorf("parse multi-port address: %w", err)
		}
		for _, tag := range memberTags {
			meta := metadata[tag]
			if meta.Port == 0 {
				continue
			}
			perMeta := map[string]poolout.MemberMeta{tag: meta}
			poolTag := fmt.Sprintf("%s-%s", poolout.Tag, tag)
			perOptions := poolout.Options{
				Mode:              "sequential",
				Members:           []string{tag},
				FailureThreshold:  cfg.Pool.FailureThreshold,
				BlacklistDuration: cfg.Pool.BlacklistDuration,
				Metadata:          perMeta,
			}
			perPool := option.Outbound{
				Type:    poolout.Type,
				Tag:     poolTag,
				Options: &perOptions,
			}
			outbounds = append(outbounds, perPool)
			username := cfg.MultiPort.Username
			password := cfg.MultiPort.Password
			protocol := cfg.MultiPort.Protocol
			if nodeCfg, ok := nodesByTag[tag]; ok {
				if nodeCfg.Username != "" {
					username = nodeCfg.Username
					password = nodeCfg.Password
				}
				if nodeCfg.InboundProtocol != "" {
					protocol = nodeCfg.InboundProtocol
				}
			}
			inboundTag := fmt.Sprintf("in-%s", tag)
			inbound, err := buildInboundByProtocol(protocol, addr, meta.Port, username, password, inboundTag)
			if err != nil {
				return option.Options{}, fmt.Errorf("build multi-port inbound %q: %w", tag, err)
			}
			inbounds = append(inbounds, inbound)
			route.Rules = append(route.Rules, option.Rule{
				Type: C.RuleTypeDefault,
				DefaultOptions: option.DefaultRule{
					RawDefaultRule: option.RawDefaultRule{
						Inbound: badoption.Listable[string]{inboundTag},
					},
					RuleAction: option.RuleAction{
						Action: C.RuleActionTypeRoute,
						RouteOptions: option.RouteActionOptions{
							Outbound: poolTag,
						},
					},
				},
			})
		}
	}

	// Build GeoIP region-based pool outbounds and routing
	if cfg.GeoIP.Enabled && firstPoolTag != "" {
		for _, proxyPool := range proxyPools {
			if !proxyPool.Enabled {
				continue
			}
			poolMembers := proxyPoolMembers(proxyPool, memberTags, nodeIDToTag)
			if len(poolMembers) == 0 {
				continue
			}
			poolMemberSet := make(map[string]struct{}, len(poolMembers))
			for _, tag := range poolMembers {
				poolMemberSet[tag] = struct{}{}
			}
			for _, region := range geoip.AllRegions() {
				members := intersectMembers(regionMembers[region], poolMemberSet)
				if len(members) == 0 {
					continue
				}
				regionPoolOptions := poolout.Options{
					Mode:              proxyPool.Mode,
					Members:           members,
					FailureThreshold:  proxyPool.FailureThreshold,
					BlacklistDuration: proxyPool.BlacklistDuration,
					Metadata:          metadataForMembers(metadata, members),
				}
				outbounds = append(outbounds, option.Outbound{
					Type:    poolout.Type,
					Tag:     proxyPoolRegionTag(proxyPool, region),
					Options: &regionPoolOptions,
				})
			}
		}

		// Log GeoIP routing info
		geoipPort := cfg.GeoIP.Port
		if geoipPort == 0 {
			geoipPort = 1221 // Default GeoIP router port
		}
		geoipListen := cfg.GeoIP.Listen
		if geoipListen == "" {
			if len(proxyPools) > 0 {
				geoipListen = proxyPools[0].Listener.Address
			} else {
				geoipListen = cfg.Listener.Address
			}
		}
		log.Println("🌐 GeoIP Region Routing Enabled:")
		log.Printf("   Access via: http://%s:%d/{pool}/{region}", geoipListen, geoipPort)
		log.Println("   Available regions: /jp, /kr, /us, /hk, /tw, /sg, /other")
		log.Println("   Default (no path): all nodes pool")
	}

	if len(inbounds) == 0 {
		return option.Options{}, errors.New("no enabled proxy listeners available")
	}

	opts := option.Options{
		Log:       &option.LogOptions{Level: strings.ToLower(cfg.LogLevel)},
		Inbounds:  inbounds,
		Outbounds: outbounds,
		Route:     &route,
		Experimental: &option.ExperimentalOptions{
			ClashAPI: &option.ClashAPIOptions{
				ExternalController: "127.0.0.1:9092",
			},
		},
	}
	if dnsOptions := buildDNSOptions(cfg.DNS); dnsOptions != nil {
		opts.DNS = dnsOptions
	}
	return opts, nil
}

func buildDNSOptions(cfg config.DNSConfig) *option.DNSOptions {
	if !cfg.Enabled {
		return nil
	}

	strategy := buildDNSDomainStrategy(cfg.Strategy)
	servers := make([]option.DNSServerOptions, 0, 1+len(cfg.FallbackServers))
	added := make(map[string]bool)

	addServer := func(tag, server string) {
		server = strings.TrimSpace(server)
		if server == "" || added[server] {
			return
		}
		added[server] = true
		servers = append(servers, option.DNSServerOptions{
			Type: C.DNSTypeUDP,
			Tag:  tag,
			Options: &option.RemoteDNSServerOptions{
				DNSServerAddressOptions: option.DNSServerAddressOptions{
					Server:     server,
					ServerPort: cfg.Port,
				},
			},
		})
	}

	addServer("dns-primary", cfg.Server)
	for idx, server := range cfg.FallbackServers {
		addServer(fmt.Sprintf("dns-fallback-%d", idx+1), server)
	}
	if len(servers) == 0 {
		return nil
	}

	return &option.DNSOptions{
		RawDNSOptions: option.RawDNSOptions{
			Servers: servers,
			Final:   servers[0].Tag,
			DNSClientOptions: option.DNSClientOptions{
				Strategy: strategy,
			},
		},
	}
}

func buildDNSDomainStrategy(value string) option.DomainStrategy {
	normalized, err := config.NormalizeDNSStrategy(value)
	if err != nil {
		normalized = config.DNSStrategyPreferIPv4
	}
	switch normalized {
	case config.DNSStrategyAsIs:
		return option.DomainStrategy(C.DomainStrategyAsIS)
	case config.DNSStrategyPreferIPv6:
		return option.DomainStrategy(C.DomainStrategyPreferIPv6)
	case config.DNSStrategyIPv4Only:
		return option.DomainStrategy(C.DomainStrategyIPv4Only)
	case config.DNSStrategyIPv6Only:
		return option.DomainStrategy(C.DomainStrategyIPv6Only)
	default:
		return option.DomainStrategy(C.DomainStrategyPreferIPv4)
	}
}

func geoIPResolverConfig(cfg config.DNSConfig) geoip.ResolverConfig {
	return geoip.ResolverConfig{
		Enabled:         cfg.Enabled,
		Server:          cfg.Server,
		FallbackServers: cfg.FallbackServers,
		Port:            cfg.Port,
		Strategy:        cfg.Strategy,
	}
}

func effectiveProxyPools(cfg *config.Config) []config.ProxyPoolConfig {
	if len(cfg.ProxyPools) > 0 {
		return cfg.ProxyPools
	}
	return []config.ProxyPoolConfig{{
		Name:              "legacy",
		Enabled:           true,
		Listener:          cfg.Listener,
		Mode:              cfg.Pool.Mode,
		FailureThreshold:  cfg.Pool.FailureThreshold,
		BlacklistDuration: cfg.Pool.BlacklistDuration,
		AllNodes:          true,
	}}
}

func proxyPoolTag(pool config.ProxyPoolConfig) string {
	if pool.ID == 0 {
		return poolout.Tag
	}
	if pool.ID > 0 {
		return fmt.Sprintf("proxy-pool-%d", pool.ID)
	}
	return fmt.Sprintf("proxy-pool-%s", sanitizeTag(pool.Name))
}

func proxyPoolInboundTag(pool config.ProxyPoolConfig) string {
	if pool.ID == 0 {
		return "http-in"
	}
	return proxyPoolTag(pool) + "-in"
}

func proxyPoolRegionTag(pool config.ProxyPoolConfig, region string) string {
	if pool.ID == 0 {
		return fmt.Sprintf("pool-%s", region)
	}
	return fmt.Sprintf("%s-%s", proxyPoolTag(pool), region)
}

func proxyPoolMembers(pool config.ProxyPoolConfig, allTags []string, nodeIDToTag map[int64]string) []string {
	if pool.AllNodes || len(pool.NodeIDs) == 0 {
		return append([]string(nil), allTags...)
	}
	members := make([]string, 0, len(pool.NodeIDs))
	seen := make(map[string]struct{}, len(pool.NodeIDs))
	for _, nodeID := range pool.NodeIDs {
		tag := nodeIDToTag[nodeID]
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		members = append(members, tag)
	}
	return members
}

func metadataForMembers(metadata map[string]poolout.MemberMeta, members []string) map[string]poolout.MemberMeta {
	out := make(map[string]poolout.MemberMeta, len(members))
	for _, tag := range members {
		out[tag] = metadata[tag]
	}
	return out
}

func intersectMembers(candidates []string, allowed map[string]struct{}) []string {
	members := make([]string, 0, len(candidates))
	for _, tag := range candidates {
		if _, ok := allowed[tag]; ok {
			members = append(members, tag)
		}
	}
	return members
}

func hasNodeLocalListeners(nodes []config.NodeConfig) bool {
	for _, node := range nodes {
		if node.Port > 0 {
			return true
		}
	}
	return false
}

func buildPoolInbound(cfg *config.Config) (option.Inbound, error) {
	listenAddr, err := parseAddr(cfg.Listener.Address)
	if err != nil {
		return option.Inbound{}, fmt.Errorf("parse listener address: %w", err)
	}
	return buildInboundByProtocol(
		cfg.Listener.Protocol,
		listenAddr,
		cfg.Listener.Port,
		cfg.Listener.Username,
		cfg.Listener.Password,
		"http-in",
	)
}

func buildInboundFromListener(listener config.ListenerConfig, tag string) (option.Inbound, error) {
	listenAddr, err := parseAddr(listener.Address)
	if err != nil {
		return option.Inbound{}, fmt.Errorf("parse listener address: %w", err)
	}
	return buildInboundByProtocol(listener.Protocol, listenAddr, listener.Port, listener.Username, listener.Password, tag)
}

func buildInboundByProtocol(protocol string, listenAddr *badoption.Addr, port uint16, username, password, tag string) (option.Inbound, error) {
	normalized, err := config.NormalizeInboundProtocol(protocol)
	if err != nil {
		return option.Inbound{}, err
	}
	listenOptions := option.ListenOptions{
		Listen:     listenAddr,
		ListenPort: port,
	}
	users := []auth.User(nil)
	if username != "" {
		users = []auth.User{{Username: username, Password: password}}
	}

	switch normalized {
	case config.InboundProtocolHTTP:
		opts := &option.HTTPMixedInboundOptions{ListenOptions: listenOptions}
		if len(users) > 0 {
			opts.Users = users
		}
		return option.Inbound{Type: C.TypeHTTP, Tag: tag, Options: opts}, nil
	case config.InboundProtocolSOCKS5:
		opts := &option.SocksInboundOptions{ListenOptions: listenOptions}
		if len(users) > 0 {
			opts.Users = users
		}
		return option.Inbound{Type: C.TypeSOCKS, Tag: tag, Options: opts}, nil
	case config.InboundProtocolMixed:
		opts := &option.HTTPMixedInboundOptions{ListenOptions: listenOptions}
		if len(users) > 0 {
			opts.Users = users
		}
		return option.Inbound{Type: C.TypeMixed, Tag: tag, Options: opts}, nil
	default:
		return option.Inbound{}, fmt.Errorf("unsupported inbound protocol %q", protocol)
	}
}

func buildNodeConfigOutbound(tag string, node config.NodeConfig, skipCertVerify bool) (option.Outbound, error) {
	if strings.TrimSpace(node.OutboundJSON) != "" {
		return outboundFromJSON(tag, node.OutboundJSON)
	}
	return buildNodeOutbound(tag, node.URI, skipCertVerify)
}

// NormalizeOutboundJSON validates sing-box outbound JSON and returns a formatted
// object. The returned object uses tag as its runtime tag regardless of input.
func NormalizeOutboundJSON(tag, rawJSON string) (string, error) {
	outbound, err := outboundFromJSON(tag, rawJSON)
	if err != nil {
		return "", err
	}
	return marshalOutboundJSON(outbound)
}

// OutboundJSONFromURI converts a supported proxy URI into sing-box outbound JSON.
func OutboundJSONFromURI(tag, rawURI string, skipCertVerify bool) (string, error) {
	outbound, err := buildNodeOutbound(tag, rawURI, skipCertVerify)
	if err != nil {
		return "", err
	}
	return marshalOutboundJSON(outbound)
}

func outboundFromJSON(tag, rawJSON string) (option.Outbound, error) {
	rawJSON = strings.TrimSpace(rawJSON)
	if rawJSON == "" {
		return option.Outbound{}, errors.New("outbound_json is empty")
	}
	ctx := include.Context(context.Background())
	var outbound option.Outbound
	if err := outbound.UnmarshalJSONContext(ctx, []byte(rawJSON)); err != nil {
		return option.Outbound{}, fmt.Errorf("parse outbound_json: %w", err)
	}
	if outbound.Type == "" {
		return option.Outbound{}, errors.New("outbound_json missing type")
	}
	outbound.Tag = tag
	return outbound, nil
}

func marshalOutboundJSON(outbound option.Outbound) (string, error) {
	data, err := boxjson.MarshalContext(include.Context(context.Background()), &outbound)
	if err != nil {
		return "", fmt.Errorf("encode outbound_json: %w", err)
	}
	var normalized any
	if err := json.Unmarshal(data, &normalized); err != nil {
		return "", fmt.Errorf("normalize outbound_json: %w", err)
	}
	data, err = json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return "", fmt.Errorf("format outbound_json: %w", err)
	}
	return string(data), nil
}

func buildNodeOutbound(tag, rawURI string, skipCertVerify bool) (option.Outbound, error) {
	parsed, err := url.Parse(rawURI)
	if err != nil {
		normalizedURI, normalized := normalizeHysteria2PortHoppingURI(rawURI)
		if !normalized {
			return option.Outbound{}, fmt.Errorf("parse uri: %w", err)
		}
		parsed, err = url.Parse(normalizedURI)
		if err != nil {
			return option.Outbound{}, fmt.Errorf("parse uri: %w", err)
		}
	}
	switch strings.ToLower(parsed.Scheme) {
	case "vless":
		opts, err := buildVLESSOptions(parsed, skipCertVerify)
		if err != nil {
			return option.Outbound{}, err
		}
		return option.Outbound{Type: C.TypeVLESS, Tag: tag, Options: &opts}, nil
	case "hysteria2", "hy2":
		opts, err := buildHysteria2Options(parsed, skipCertVerify)
		if err != nil {
			return option.Outbound{}, err
		}
		return option.Outbound{Type: C.TypeHysteria2, Tag: tag, Options: &opts}, nil
	case "ss", "shadowsocks":
		opts, err := buildShadowsocksOptions(parsed)
		if err != nil {
			return option.Outbound{}, err
		}
		return option.Outbound{Type: C.TypeShadowsocks, Tag: tag, Options: &opts}, nil
	case "trojan":
		opts, err := buildTrojanOptions(parsed, skipCertVerify)
		if err != nil {
			return option.Outbound{}, err
		}
		return option.Outbound{Type: C.TypeTrojan, Tag: tag, Options: &opts}, nil
	case "anytls":
		opts, err := buildAnyTLSOptions(parsed, skipCertVerify)
		if err != nil {
			return option.Outbound{}, err
		}
		return option.Outbound{Type: C.TypeAnyTLS, Tag: tag, Options: &opts}, nil
	case "tuic":
		opts, err := buildTUICOptions(parsed, skipCertVerify)
		if err != nil {
			return option.Outbound{}, err
		}
		return option.Outbound{Type: C.TypeTUIC, Tag: tag, Options: &opts}, nil
	case "vmess":
		opts, err := buildVMessOptions(rawURI, skipCertVerify)
		if err != nil {
			return option.Outbound{}, err
		}
		return option.Outbound{Type: C.TypeVMess, Tag: tag, Options: &opts}, nil
	case "socks5", "socks":
		opts, err := buildSOCKSOptions(parsed)
		if err != nil {
			return option.Outbound{}, err
		}
		return option.Outbound{Type: C.TypeSOCKS, Tag: tag, Options: &opts}, nil
	case "http", "https":
		opts, err := buildHTTPProxyOptions(parsed, skipCertVerify)
		if err != nil {
			return option.Outbound{}, err
		}
		return option.Outbound{Type: C.TypeHTTP, Tag: tag, Options: &opts}, nil
	default:
		return option.Outbound{}, fmt.Errorf("unsupported scheme %q", parsed.Scheme)
	}
}

func buildVLESSOptions(u *url.URL, skipCertVerify bool) (option.VLESSOutboundOptions, error) {
	uuid := u.User.Username()
	if uuid == "" {
		return option.VLESSOutboundOptions{}, errors.New("vless uri missing uuid in userinfo")
	}
	server, port, err := hostPort(u, 443)
	if err != nil {
		return option.VLESSOutboundOptions{}, err
	}
	query := u.Query()

	// Pre-validate flow - reject unsupported XTLS flows
	if flow := query.Get("flow"); flow != "" {
		unsupportedFlows := []string{"xtls-rprx-direct", "xtls-rprx-origin", "xtls-rprx-splice"}
		flowLower := strings.ToLower(flow)
		for _, unsupported := range unsupportedFlows {
			if flowLower == unsupported {
				return option.VLESSOutboundOptions{}, fmt.Errorf("unsupported flow: %s (deprecated XTLS)", flow)
			}
		}
	}

	opts := option.VLESSOutboundOptions{
		UUID:          uuid,
		ServerOptions: option.ServerOptions{Server: server, ServerPort: uint16(port)},
		Network:       option.NetworkList(""),
	}
	if flow := query.Get("flow"); flow != "" {
		opts.Flow = flow
	}
	if packetEncoding := query.Get("packetEncoding"); packetEncoding != "" {
		opts.PacketEncoding = &packetEncoding
	}
	if transport, err := buildV2RayTransport(query); err != nil {
		return option.VLESSOutboundOptions{}, err
	} else if transport != nil {
		opts.Transport = transport
	}
	if tlsOptions, err := buildTLSOptions(query, skipCertVerify); err != nil {
		return option.VLESSOutboundOptions{}, err
	} else if tlsOptions != nil {
		opts.OutboundTLSOptionsContainer = option.OutboundTLSOptionsContainer{TLS: tlsOptions}
	}
	return opts, nil
}

func buildHysteria2Options(u *url.URL, skipCertVerify bool) (option.Hysteria2OutboundOptions, error) {
	password := u.User.String()
	server, port, hopPorts, err := hysteria2HostPort(u, 443)
	if err != nil {
		return option.Hysteria2OutboundOptions{}, err
	}
	query := u.Query()
	hopPorts = appendUniqueStrings(hopPorts, parseHysteria2Ports(query.Get("ports"))...)
	hopPorts = appendUniqueStrings(hopPorts, parseHysteria2Ports(query.Get("server_ports"))...)
	hopPorts = appendUniqueStrings(hopPorts, parseHysteria2Ports(query.Get("mport"))...)
	opts := option.Hysteria2OutboundOptions{
		ServerOptions: option.ServerOptions{Server: server, ServerPort: uint16(port)},
		Password:      password,
	}
	if len(hopPorts) > 0 {
		opts.ServerPorts = badoption.Listable[string](hopPorts)
	}
	if hopInterval := query.Get("hop_interval"); hopInterval != "" {
		d, err := time.ParseDuration(hopInterval)
		if err != nil {
			return option.Hysteria2OutboundOptions{}, fmt.Errorf("invalid hop_interval %q: %w", hopInterval, err)
		}
		opts.HopInterval = badoption.Duration(d)
	} else if hopInterval := query.Get("hopInterval"); hopInterval != "" {
		d, err := time.ParseDuration(hopInterval)
		if err != nil {
			return option.Hysteria2OutboundOptions{}, fmt.Errorf("invalid hopInterval %q: %w", hopInterval, err)
		}
		opts.HopInterval = badoption.Duration(d)
	}
	if up := query.Get("upMbps"); up != "" {
		opts.UpMbps = atoiDefault(up)
	}
	if down := query.Get("downMbps"); down != "" {
		opts.DownMbps = atoiDefault(down)
	}
	if obfs := query.Get("obfs"); obfs != "" {
		opts.Obfs = &option.Hysteria2Obfs{Type: obfs, Password: query.Get("obfs-password")}
	}
	opts.OutboundTLSOptionsContainer = option.OutboundTLSOptionsContainer{TLS: hysteriaTLSOptions(server, query, skipCertVerify)}
	return opts, nil
}

func hysteriaTLSOptions(host string, query url.Values, skipCertVerify bool) *option.OutboundTLSOptions {
	tlsOptions := &option.OutboundTLSOptions{
		Enabled:    true,
		ServerName: host,
		Insecure:   skipCertVerify,
	}
	if sni := query.Get("sni"); sni != "" {
		tlsOptions.ServerName = sni
	}
	insecure := query.Get("insecure")
	if insecure == "" {
		insecure = query.Get("allowInsecure")
	}
	if insecure != "" {
		tlsOptions.Insecure = insecure == "1" || strings.EqualFold(insecure, "true")
	}
	if alpn := query.Get("alpn"); alpn != "" {
		tlsOptions.ALPN = badoption.Listable[string](strings.Split(alpn, ","))
	}
	return tlsOptions
}

func buildTLSOptions(query url.Values, skipCertVerify bool) (*option.OutboundTLSOptions, error) {
	security := strings.ToLower(query.Get("security"))
	if security == "" || security == "none" {
		return nil, nil
	}
	tlsOptions := &option.OutboundTLSOptions{Enabled: true, Insecure: skipCertVerify}
	if sni := query.Get("sni"); sni != "" {
		tlsOptions.ServerName = sni
	}
	insecure := query.Get("allowInsecure")
	if insecure == "" {
		insecure = query.Get("insecure")
	}
	if insecure != "" {
		tlsOptions.Insecure = insecure == "1" || strings.EqualFold(insecure, "true")
	}
	if alpn := query.Get("alpn"); alpn != "" {
		tlsOptions.ALPN = badoption.Listable[string](strings.Split(alpn, ","))
	}
	fp := query.Get("fp")
	if fp != "" {
		tlsOptions.UTLS = &option.OutboundUTLSOptions{Enabled: true, Fingerprint: fp}
	}
	if security == "reality" {
		pbk := query.Get("pbk")
		// Validate reality public key - must be valid base64 and 32 bytes (43-44 chars base64)
		if pbk == "" {
			return nil, fmt.Errorf("reality security requires public_key (pbk parameter)")
		}
		// Try to decode the public key to validate it
		decoded, err := base64.RawURLEncoding.DecodeString(pbk)
		if err != nil {
			decoded, err = base64.StdEncoding.DecodeString(pbk)
			if err != nil {
				return nil, fmt.Errorf("invalid reality public_key: %w", err)
			}
		}
		if len(decoded) != 32 {
			return nil, fmt.Errorf("invalid reality public_key: expected 32 bytes, got %d", len(decoded))
		}
		tlsOptions.Reality = &option.OutboundRealityOptions{Enabled: true, PublicKey: pbk, ShortID: query.Get("sid")}
		// Reality requires uTLS; use default fingerprint if not specified
		if tlsOptions.UTLS == nil {
			if fp == "" {
				fp = "chrome"
			}
			tlsOptions.UTLS = &option.OutboundUTLSOptions{Enabled: true, Fingerprint: fp}
		}
	}
	return tlsOptions, nil
}

func buildV2RayTransport(query url.Values) (*option.V2RayTransportOptions, error) {
	transportType := strings.ToLower(query.Get("type"))
	if transportType == "" || transportType == "tcp" {
		return nil, nil
	}

	// Pre-validate transport type - reject unsupported types early
	unsupportedTransports := map[string]bool{
		"kcp":  true,
		"raw":  true,
		"quic": true, // sing-box doesn't support QUIC as V2Ray transport
	}
	if unsupportedTransports[transportType] {
		return nil, fmt.Errorf("unsupported transport type: %s", transportType)
	}

	options := &option.V2RayTransportOptions{Type: transportType}
	switch transportType {
	case C.V2RayTransportTypeWebsocket:
		wsPath := query.Get("path")
		// 解析 path 中的 early data 参数，如 /path?ed=2048
		if idx := strings.Index(wsPath, "?ed="); idx != -1 {
			edPart := wsPath[idx+4:]
			wsPath = wsPath[:idx]
			// 解析 ed 值
			edValue := edPart
			if ampIdx := strings.Index(edPart, "&"); ampIdx != -1 {
				edValue = edPart[:ampIdx]
			}
			if ed, err := strconv.Atoi(edValue); err == nil && ed > 0 {
				options.WebsocketOptions.MaxEarlyData = uint32(ed)
				options.WebsocketOptions.EarlyDataHeaderName = "Sec-WebSocket-Protocol"
			}
		}
		options.WebsocketOptions.Path = wsPath
		if host := query.Get("host"); host != "" {
			options.WebsocketOptions.Headers = badoption.HTTPHeader{"Host": {host}}
		}
	case C.V2RayTransportTypeHTTP:
		options.HTTPOptions.Path = query.Get("path")
		if host := query.Get("host"); host != "" {
			options.HTTPOptions.Host = badoption.Listable[string]{host}
		}
	case C.V2RayTransportTypeGRPC:
		options.GRPCOptions.ServiceName = query.Get("serviceName")
	case C.V2RayTransportTypeHTTPUpgrade:
		options.HTTPUpgradeOptions.Path = query.Get("path")
	case "xhttp":
		// XHTTP is not supported by sing-box, fallback to HTTPUpgrade
		log.Printf("⚠️  XHTTP transport not supported by sing-box, falling back to HTTPUpgrade")
		options.Type = C.V2RayTransportTypeHTTPUpgrade
		options.HTTPUpgradeOptions.Path = query.Get("path")
		if host := query.Get("host"); host != "" {
			options.HTTPUpgradeOptions.Headers = badoption.HTTPHeader{"Host": {host}}
		}
	default:
		return nil, fmt.Errorf("unsupported transport type %q", transportType)
	}
	return options, nil
}

func buildShadowsocksOptions(u *url.URL) (option.ShadowsocksOutboundOptions, error) {
	server, port, err := hostPort(u, 8388)
	if err != nil {
		return option.ShadowsocksOutboundOptions{}, err
	}

	// Decode userinfo (base64 encoded method:password)
	userInfo := u.User.String()
	decoded, err := base64.RawURLEncoding.DecodeString(userInfo)
	if err != nil {
		// Try standard base64
		decoded, err = base64.StdEncoding.DecodeString(userInfo)
		if err != nil {
			return option.ShadowsocksOutboundOptions{}, fmt.Errorf("decode shadowsocks userinfo: %w", err)
		}
	}

	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 {
		return option.ShadowsocksOutboundOptions{}, errors.New("shadowsocks userinfo format must be method:password")
	}

	method := normalizeShadowsocksMethod(parts[0])
	password := parts[1]

	opts := option.ShadowsocksOutboundOptions{
		ServerOptions: option.ServerOptions{Server: server, ServerPort: uint16(port)},
		Method:        method,
		Password:      password,
	}

	query := u.Query()
	if plugin := query.Get("plugin"); plugin != "" {
		// sing-box library mode doesn't support external plugins like v2ray-plugin
		// These require the plugin binary to be installed separately
		return option.ShadowsocksOutboundOptions{}, fmt.Errorf("shadowsocks plugin not supported: %s (requires external binary)", plugin)
	}

	return opts, nil
}

func buildTrojanOptions(u *url.URL, skipCertVerify bool) (option.TrojanOutboundOptions, error) {
	password := u.User.Username()
	if password == "" {
		return option.TrojanOutboundOptions{}, errors.New("trojan uri missing password in userinfo")
	}

	server, port, err := hostPort(u, 443)
	if err != nil {
		return option.TrojanOutboundOptions{}, err
	}

	query := u.Query()
	opts := option.TrojanOutboundOptions{
		ServerOptions: option.ServerOptions{Server: server, ServerPort: uint16(port)},
		Password:      password,
		Network:       option.NetworkList(""),
	}

	// Parse TLS options
	if tlsOptions, err := buildTrojanTLSOptions(query, skipCertVerify); err != nil {
		return option.TrojanOutboundOptions{}, err
	} else if tlsOptions != nil {
		opts.OutboundTLSOptionsContainer = option.OutboundTLSOptionsContainer{TLS: tlsOptions}
	}

	// Parse transport options
	if transport, err := buildV2RayTransport(query); err != nil {
		return option.TrojanOutboundOptions{}, err
	} else if transport != nil {
		opts.Transport = transport
	}

	return opts, nil
}

func buildAnyTLSOptions(u *url.URL, skipCertVerify bool) (option.AnyTLSOutboundOptions, error) {
	password := u.User.Username()
	if password == "" {
		password, _ = u.User.Password()
	}

	server, port, err := hostPort(u, 443)
	if err != nil {
		return option.AnyTLSOutboundOptions{}, err
	}

	query := u.Query()
	opts := option.AnyTLSOutboundOptions{
		ServerOptions: option.ServerOptions{Server: server, ServerPort: uint16(port)},
		Password:      password,
	}

	// Parse TLS options
	if tlsOptions, err := buildTLSOptions(query, skipCertVerify); err != nil {
		return option.AnyTLSOutboundOptions{}, err
	} else if tlsOptions != nil {
		opts.OutboundTLSOptionsContainer = option.OutboundTLSOptionsContainer{TLS: tlsOptions}
	} else {
		// AnyTLS defaults to TLS enabled
		opts.OutboundTLSOptionsContainer = option.OutboundTLSOptionsContainer{
			TLS: &option.OutboundTLSOptions{
				Enabled:    true,
				ServerName: server,
				Insecure:   skipCertVerify,
			},
		}
	}

	return opts, nil
}

func buildTUICOptions(u *url.URL, skipCertVerify bool) (option.TUICOutboundOptions, error) {
	uuid := u.User.Username()
	password, _ := u.User.Password()

	server, port, err := hostPort(u, 443)
	if err != nil {
		return option.TUICOutboundOptions{}, err
	}

	query := u.Query()
	opts := option.TUICOutboundOptions{
		ServerOptions: option.ServerOptions{Server: server, ServerPort: uint16(port)},
		UUID:          uuid,
		Password:      password,
	}

	// Congestion control (bbr, cubic, new_reno)
	if cc := query.Get("congestion_control"); cc != "" {
		opts.CongestionControl = cc
	}

	// UDP relay mode (native, quic)
	if udpMode := query.Get("udp_relay_mode"); udpMode != "" {
		opts.UDPRelayMode = udpMode
	}

	// TLS options (TUIC always uses TLS)
	tlsOptions := &option.OutboundTLSOptions{
		Enabled:    true,
		ServerName: server,
		Insecure:   skipCertVerify,
	}
	if sni := query.Get("sni"); sni != "" {
		tlsOptions.ServerName = sni
	}
	insecure := query.Get("allowInsecure")
	if insecure == "" {
		insecure = query.Get("insecure")
	}
	if insecure != "" {
		tlsOptions.Insecure = insecure == "1" || strings.EqualFold(insecure, "true")
	}
	if alpn := query.Get("alpn"); alpn != "" {
		tlsOptions.ALPN = badoption.Listable[string](strings.Split(alpn, ","))
	}
	opts.OutboundTLSOptionsContainer = option.OutboundTLSOptionsContainer{TLS: tlsOptions}

	return opts, nil
}

// vmessJSON represents the JSON structure of a VMess URI
type vmessJSON struct {
	V    interface{} `json:"v"`    // Version, can be string or int
	PS   string      `json:"ps"`   // Remarks/name
	Add  string      `json:"add"`  // Server address
	Port interface{} `json:"port"` // Server port, can be string or int
	ID   string      `json:"id"`   // UUID
	Aid  interface{} `json:"aid"`  // Alter ID, can be string or int
	Scy  string      `json:"scy"`  // Security/cipher
	Net  string      `json:"net"`  // Network type (tcp, ws, etc.)
	Type string      `json:"type"` // Header type
	Host string      `json:"host"` // Host header
	Path string      `json:"path"` // Path
	TLS  string      `json:"tls"`  // TLS (tls or empty)
	SNI  string      `json:"sni"`  // SNI
	ALPN string      `json:"alpn"` // ALPN
	FP   string      `json:"fp"`   // Fingerprint
}

func (v *vmessJSON) GetPort() int {
	switch p := v.Port.(type) {
	case float64:
		return int(p)
	case int:
		return p
	case string:
		port, _ := strconv.Atoi(p)
		return port
	}
	return 443
}

func (v *vmessJSON) GetAlterId() int {
	switch a := v.Aid.(type) {
	case float64:
		return int(a)
	case int:
		return a
	case string:
		aid, _ := strconv.Atoi(a)
		return aid
	}
	return 0
}

func buildVMessOptions(rawURI string, skipCertVerify bool) (option.VMessOutboundOptions, error) {
	// Remove vmess:// prefix
	encoded := strings.TrimPrefix(rawURI, "vmess://")

	// Try to decode as base64 JSON (standard format)
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		// Try URL-safe base64
		decoded, err = base64.RawURLEncoding.DecodeString(encoded)
		if err != nil {
			// Try as URL format: vmess://uuid@server:port?...
			return buildVMessOptionsFromURL(rawURI, skipCertVerify)
		}
	}

	var vmess vmessJSON
	if err := json.Unmarshal(decoded, &vmess); err != nil {
		return option.VMessOutboundOptions{}, fmt.Errorf("parse vmess json: %w", err)
	}

	if vmess.Add == "" {
		return option.VMessOutboundOptions{}, errors.New("vmess missing server address")
	}
	if vmess.ID == "" {
		return option.VMessOutboundOptions{}, errors.New("vmess missing uuid")
	}

	port := vmess.GetPort()
	if port == 0 {
		port = 443
	}

	opts := option.VMessOutboundOptions{
		ServerOptions: option.ServerOptions{
			Server:     vmess.Add,
			ServerPort: uint16(port),
		},
		UUID:     vmess.ID,
		AlterId:  vmess.GetAlterId(),
		Security: vmess.Scy,
	}

	// Default security
	if opts.Security == "" {
		opts.Security = "auto"
	}

	// Build transport options
	if vmess.Net != "" && vmess.Net != "tcp" {
		transport := &option.V2RayTransportOptions{}
		switch vmess.Net {
		case "ws":
			transport.Type = C.V2RayTransportTypeWebsocket
			wsPath := vmess.Path
			// Handle early data in path
			if idx := strings.Index(wsPath, "?ed="); idx != -1 {
				edPart := wsPath[idx+4:]
				wsPath = wsPath[:idx]
				edValue := edPart
				if ampIdx := strings.Index(edPart, "&"); ampIdx != -1 {
					edValue = edPart[:ampIdx]
				}
				if ed, err := strconv.Atoi(edValue); err == nil && ed > 0 {
					transport.WebsocketOptions.MaxEarlyData = uint32(ed)
					transport.WebsocketOptions.EarlyDataHeaderName = "Sec-WebSocket-Protocol"
				}
			}
			transport.WebsocketOptions.Path = wsPath
			if vmess.Host != "" {
				transport.WebsocketOptions.Headers = badoption.HTTPHeader{"Host": {vmess.Host}}
			}
		case "h2":
			transport.Type = C.V2RayTransportTypeHTTP
			transport.HTTPOptions.Path = vmess.Path
			if vmess.Host != "" {
				transport.HTTPOptions.Host = badoption.Listable[string]{vmess.Host}
			}
		case "grpc":
			transport.Type = C.V2RayTransportTypeGRPC
			transport.GRPCOptions.ServiceName = vmess.Path
		default:
			transport.Type = vmess.Net
		}
		opts.Transport = transport
	}

	// Build TLS options
	if vmess.TLS == "tls" {
		tlsOptions := &option.OutboundTLSOptions{Enabled: true, Insecure: skipCertVerify}
		if vmess.SNI != "" {
			tlsOptions.ServerName = vmess.SNI
		} else if vmess.Host != "" {
			tlsOptions.ServerName = vmess.Host
		}
		if vmess.ALPN != "" {
			tlsOptions.ALPN = badoption.Listable[string](strings.Split(vmess.ALPN, ","))
		}
		if vmess.FP != "" {
			tlsOptions.UTLS = &option.OutboundUTLSOptions{Enabled: true, Fingerprint: vmess.FP}
		}
		opts.OutboundTLSOptionsContainer = option.OutboundTLSOptionsContainer{TLS: tlsOptions}
	}

	return opts, nil
}

func buildVMessOptionsFromURL(rawURI string, skipCertVerify bool) (option.VMessOutboundOptions, error) {
	parsed, err := url.Parse(rawURI)
	if err != nil {
		return option.VMessOutboundOptions{}, fmt.Errorf("parse vmess url: %w", err)
	}

	uuid := parsed.User.Username()
	if uuid == "" {
		return option.VMessOutboundOptions{}, errors.New("vmess uri missing uuid")
	}

	server, port, err := hostPort(parsed, 443)
	if err != nil {
		return option.VMessOutboundOptions{}, err
	}

	query := parsed.Query()
	opts := option.VMessOutboundOptions{
		ServerOptions: option.ServerOptions{
			Server:     server,
			ServerPort: uint16(port),
		},
		UUID:     uuid,
		Security: query.Get("encryption"),
	}

	if opts.Security == "" {
		opts.Security = "auto"
	}

	if aid := query.Get("alterId"); aid != "" {
		opts.AlterId, _ = strconv.Atoi(aid)
	}

	// Build transport
	if transport, err := buildV2RayTransport(query); err != nil {
		return option.VMessOutboundOptions{}, err
	} else if transport != nil {
		opts.Transport = transport
	}

	// Build TLS
	if tlsOptions, err := buildTLSOptions(query, skipCertVerify); err != nil {
		return option.VMessOutboundOptions{}, err
	} else if tlsOptions != nil {
		opts.OutboundTLSOptionsContainer = option.OutboundTLSOptionsContainer{TLS: tlsOptions}
	}

	return opts, nil
}

func buildTrojanTLSOptions(query url.Values, skipCertVerify bool) (*option.OutboundTLSOptions, error) {
	// Trojan always uses TLS by default
	tlsOptions := &option.OutboundTLSOptions{Enabled: true, Insecure: skipCertVerify}

	if sni := query.Get("sni"); sni != "" {
		tlsOptions.ServerName = sni
	}
	if peer := query.Get("peer"); peer != "" && tlsOptions.ServerName == "" {
		tlsOptions.ServerName = peer
	}

	insecure := query.Get("allowInsecure")
	if insecure == "" {
		insecure = query.Get("insecure")
	}
	if insecure != "" {
		tlsOptions.Insecure = insecure == "1" || strings.EqualFold(insecure, "true")
	}

	if alpn := query.Get("alpn"); alpn != "" {
		tlsOptions.ALPN = badoption.Listable[string](strings.Split(alpn, ","))
	}

	if fp := query.Get("fp"); fp != "" {
		tlsOptions.UTLS = &option.OutboundUTLSOptions{Enabled: true, Fingerprint: fp}
	}

	return tlsOptions, nil
}

func hostPort(u *url.URL, defaultPort int) (string, int, error) {
	host := u.Hostname()
	if host == "" {
		return "", 0, errors.New("missing host")
	}
	portStr := u.Port()
	if portStr == "" {
		portStr = strconv.Itoa(defaultPort)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", 0, fmt.Errorf("invalid port %q", portStr)
	}
	return host, port, nil
}

func normalizeHysteria2PortHoppingURI(rawURI string) (string, bool) {
	lowerURI := strings.ToLower(rawURI)
	if !strings.HasPrefix(lowerURI, "hysteria2://") && !strings.HasPrefix(lowerURI, "hy2://") {
		return "", false
	}

	schemeSep := strings.Index(rawURI, "://")
	if schemeSep == -1 {
		return "", false
	}

	scheme := rawURI[:schemeSep]
	rest := rawURI[schemeSep+3:]

	fragment := ""
	if idx := strings.Index(rest, "#"); idx != -1 {
		fragment = rest[idx:]
		rest = rest[:idx]
	}

	rawQuery := ""
	if idx := strings.Index(rest, "?"); idx != -1 {
		rawQuery = rest[idx+1:]
		rest = rest[:idx]
	}

	atIdx := strings.LastIndex(rest, "@")
	if atIdx == -1 {
		return "", false
	}
	userInfo := rest[:atIdx]
	authority := rest[atIdx+1:]

	portSep := strings.LastIndex(authority, ":")
	if portSep == -1 {
		return "", false
	}
	host := authority[:portSep]
	rawPort := strings.TrimSpace(authority[portSep+1:])
	if host == "" || rawPort == "" {
		return "", false
	}

	if _, err := strconv.Atoi(rawPort); err == nil {
		return "", false
	}
	if !looksLikeHysteria2PortSet(rawPort) {
		return "", false
	}

	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		values = url.Values{}
	}
	if strings.TrimSpace(values.Get("ports")) == "" && strings.TrimSpace(values.Get("server_ports")) == "" && strings.TrimSpace(values.Get("mport")) == "" {
		values.Set("ports", rawPort)
	}

	normalizedURI := fmt.Sprintf("%s://%s@%s:%d", scheme, userInfo, host, 443)
	if encoded := values.Encode(); encoded != "" {
		normalizedURI += "?" + encoded
	}
	normalizedURI += fragment

	return normalizedURI, true
}

func looksLikeHysteria2PortSet(v string) bool {
	if v == "" {
		return false
	}
	for _, r := range v {
		if (r >= '0' && r <= '9') || r == '-' || r == ',' {
			continue
		}
		return false
	}
	return true
}

func hysteria2HostPort(u *url.URL, defaultPort int) (string, int, []string, error) {
	host := u.Hostname()
	if host == "" {
		return "", 0, nil, errors.New("missing host")
	}

	port := defaultPort
	var hopPorts []string
	rawPort := strings.TrimSpace(u.Port())
	if rawPort == "" {
		return host, port, hopPorts, nil
	}

	if numericPort, err := strconv.Atoi(rawPort); err == nil {
		return host, numericPort, hopPorts, nil
	}

	hopPorts = append(hopPorts, rawPort)
	return host, port, hopPorts, nil
}

func parseHysteria2Ports(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	ports := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			ports = append(ports, normalizeHysteria2PortRange(part))
		}
	}
	return ports
}

func normalizeHysteria2PortRange(portRange string) string {
	if strings.Contains(portRange, ":") {
		return portRange
	}
	if strings.Count(portRange, "-") == 1 {
		return strings.Replace(portRange, "-", ":", 1)
	}
	return portRange
}

func appendUniqueStrings(base []string, values ...string) []string {
	if len(values) == 0 {
		return base
	}
	seen := make(map[string]struct{}, len(base))
	for _, item := range base {
		seen[item] = struct{}{}
	}
	for _, item := range values {
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		base = append(base, item)
	}
	return base
}

// normalizeShadowsocksMethod maps common Shadowsocks method aliases to the
// canonical names expected by sing-box.
func normalizeShadowsocksMethod(method string) string {
	aliases := map[string]string{
		"chacha20-poly1305": "chacha20-ietf-poly1305",
		"chacha20":          "chacha20-ietf",
		"auto":              "aes-128-gcm", // "auto" is not valid in sing-box, default to aes-128-gcm
	}
	if canonical, ok := aliases[strings.ToLower(method)]; ok {
		return canonical
	}
	return method
}

func buildSOCKSOptions(u *url.URL) (option.SOCKSOutboundOptions, error) {
	server, port, err := hostPort(u, 1080)
	if err != nil {
		return option.SOCKSOutboundOptions{}, err
	}
	opts := option.SOCKSOutboundOptions{
		ServerOptions: option.ServerOptions{Server: server, ServerPort: uint16(port)},
		Version:       "5",
		Network:       option.NetworkList(""),
	}
	if u.User != nil {
		opts.Username = u.User.Username()
		if pass, ok := u.User.Password(); ok {
			opts.Password = pass
		}
	}
	return opts, nil
}

func buildHTTPProxyOptions(u *url.URL, skipCertVerify bool) (option.HTTPOutboundOptions, error) {
	server, port, err := hostPort(u, 8080)
	if err != nil {
		return option.HTTPOutboundOptions{}, err
	}
	opts := option.HTTPOutboundOptions{
		ServerOptions: option.ServerOptions{Server: server, ServerPort: uint16(port)},
	}
	if u.User != nil {
		opts.Username = u.User.Username()
		if pass, ok := u.User.Password(); ok {
			opts.Password = pass
		}
	}
	if strings.ToLower(u.Scheme) == "https" {
		opts.OutboundTLSOptionsContainer = option.OutboundTLSOptionsContainer{
			TLS: &option.OutboundTLSOptions{
				Enabled:    true,
				ServerName: u.Hostname(),
				Insecure:   skipCertVerify,
			},
		}
	}
	return opts, nil
}

func parseAddr(value string) (*badoption.Addr, error) {
	addr := strings.TrimSpace(value)
	if addr == "" {
		return nil, nil
	}
	parsed, err := netip.ParseAddr(addr)
	if err != nil {
		return nil, err
	}
	bad := badoption.Addr(parsed)
	return &bad, nil
}

func sanitizeTag(name string) string {
	lower := strings.ToLower(name)
	lower = strings.TrimSpace(lower)
	if lower == "" {
		return ""
	}
	segments := strings.FieldsFunc(lower, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
	result := strings.Join(segments, "-")
	result = strings.Trim(result, "-")
	return result
}

func atoiDefault(value string) int {
	if strings.HasSuffix(value, "mbps") {
		value = strings.TrimSuffix(value, "mbps")
	}
	if strings.HasSuffix(value, "Mbps") {
		value = strings.TrimSuffix(value, "Mbps")
	}
	v, _ := strconv.Atoi(value)
	return v
}

// printProxyLinks prints all proxy connection information at startup
func printProxyLinks(cfg *config.Config, _ map[string]poolout.MemberMeta) {
	log.Println("")
	log.Println("📡 Proxy Links:")
	log.Println("═══════════════════════════════════════════════════════════════")

	for _, proxyPool := range effectiveProxyPools(cfg) {
		if !proxyPool.Enabled || proxyPool.Listener.Port == 0 {
			continue
		}
		var auth string
		if proxyPool.Listener.Username != "" {
			auth = fmt.Sprintf("%s:%s@", proxyPool.Listener.Username, proxyPool.Listener.Password)
		}
		log.Printf("🌐 Proxy Pool: %s", proxyPool.Name)
		for _, link := range proxyLinksForProtocol(proxyPool.Listener.Protocol, auth, proxyPool.Listener.Address, proxyPool.Listener.Port) {
			log.Printf("   %-7s %s", link.Label+":", link.URL)
		}
		log.Println("")
	}

	if hasNodeLocalListeners(cfg.Nodes) {
		log.Printf("🔌 Node Local Entry Points:")
		log.Println("")
		for _, node := range cfg.Nodes {
			if node.Disabled || node.Port == 0 {
				continue
			}
			var auth string
			username := node.Username
			password := node.Password
			if username == "" {
				username = cfg.MultiPort.Username
				password = cfg.MultiPort.Password
			}
			if username != "" {
				auth = fmt.Sprintf("%s:%s@", username, password)
			}
			log.Printf("   [%d] %s", node.Port, node.Name)
			protocol := cfg.MultiPort.Protocol
			if node.InboundProtocol != "" {
				protocol = node.InboundProtocol
			}
			for _, link := range proxyLinksForProtocol(protocol, auth, cfg.MultiPort.Address, node.Port) {
				log.Printf("       %-7s %s", link.Label+":", link.URL)
			}
		}
	}

	log.Println("═══════════════════════════════════════════════════════════════")
	log.Println("")
}

type proxyLink struct {
	Label string
	URL   string
}

func proxyLinksForProtocol(protocol, auth, address string, port uint16) []proxyLink {
	normalized, err := config.NormalizeInboundProtocol(protocol)
	if err != nil {
		normalized = config.InboundProtocolMixed
	}

	httpURL := fmt.Sprintf("http://%s%s:%d", auth, address, port)
	socksURL := fmt.Sprintf("socks5://%s%s:%d", auth, address, port)
	switch normalized {
	case config.InboundProtocolHTTP:
		return []proxyLink{{Label: "HTTP", URL: httpURL}}
	case config.InboundProtocolSOCKS5:
		return []proxyLink{{Label: "SOCKS5", URL: socksURL}}
	default:
		return []proxyLink{
			{Label: "HTTP", URL: httpURL},
			{Label: "SOCKS5", URL: socksURL},
		}
	}
}
