package monitor

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	mathrand "math/rand"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"easy_proxies/internal/config"
	"easy_proxies/internal/geoip"
	"easy_proxies/internal/store"
	"golang.org/x/sync/semaphore"
)

//go:embed assets/*
var embeddedFS embed.FS

// NodeManager exposes config node CRUD and reload operations.
type NodeManager interface {
	ListConfigNodes(ctx context.Context) ([]config.NodeConfig, error)
	ParseNodeURI(ctx context.Context, name string, uri string) (config.NodeConfig, error)
	CreateNode(ctx context.Context, node config.NodeConfig) (config.NodeConfig, error)
	UpdateNode(ctx context.Context, name string, node config.NodeConfig) (config.NodeConfig, error)
	SetNodeEnabled(ctx context.Context, name string, enabled bool) error
	DeleteNode(ctx context.Context, name string) error
	TriggerReload(ctx context.Context) error
}

// Sentinel errors for node operations.
var (
	ErrNodeNotFound = errors.New("节点不存在")
	ErrNodeConflict = errors.New("节点名称或端口已存在")
	ErrInvalidNode  = errors.New("无效的节点配置")
	ErrNodeReadOnly = errors.New("订阅节点不支持编辑或删除")
)

// SubscriptionRefresher interface for subscription manager.
type SubscriptionRefresher interface {
	RefreshNow() error
	RefreshSource(id int64) error
	ReloadSchedule()
	Status() SubscriptionStatus
}

// SubscriptionStatus represents subscription refresh status.
type SubscriptionStatus struct {
	LastRefresh  time.Time `json:"last_refresh"`
	NextRefresh  time.Time `json:"next_refresh"`
	NodeCount    int       `json:"node_count"`
	LastError    string    `json:"last_error,omitempty"`
	RefreshCount int       `json:"refresh_count"`
	IsRefreshing bool      `json:"is_refreshing"`
}

// Server exposes HTTP endpoints for monitoring.
type Server struct {
	cfg    Config
	cfgMu  sync.RWMutex   // 保护动态配置字段
	cfgSrc *config.Config // 可持久化的配置对象
	store  store.Store
	mgr    *Manager
	srv    *http.Server
	logger *log.Logger

	sessionTTL time.Duration

	// Concurrency control
	probeSem *semaphore.Weighted

	subRefresher SubscriptionRefresher
	nodeMgr      NodeManager
}

// NewServer constructs a server; it can be nil when disabled.
func NewServer(cfg Config, mgr *Manager, logger *log.Logger) *Server {
	if !cfg.Enabled || mgr == nil {
		return nil
	}
	if logger == nil {
		logger = log.Default()
	}

	// Calculate max concurrent probes
	maxConcurrentProbes := int64(runtime.NumCPU() * 4)
	if maxConcurrentProbes < 10 {
		maxConcurrentProbes = 10
	}

	s := &Server{
		cfg:        cfg,
		mgr:        mgr,
		logger:     logger,
		sessionTTL: 24 * time.Hour,
		probeSem:   semaphore.NewWeighted(maxConcurrentProbes),
	}

	// Start session cleanup goroutine
	go s.cleanupExpiredSessions()

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/auth", s.handleAuth)
	mux.HandleFunc("/api/settings", s.withAuth(s.handleSettings))
	mux.HandleFunc("/api/nodes", s.withAuth(s.handleNodes))
	mux.HandleFunc("/api/nodes/config", s.withAuth(s.handleConfigNodes))
	mux.HandleFunc("/api/nodes/config/parse-uri", s.withAuth(s.handleNodeURIParse))
	mux.HandleFunc("/api/nodes/config/batch-toggle", s.withAuth(s.handleConfigNodesBatchToggle))
	mux.HandleFunc("/api/nodes/config/batch-delete", s.withAuth(s.handleConfigNodesBatchDelete))
	mux.HandleFunc("/api/nodes/config/", s.withAuth(s.handleConfigNodeItem))
	mux.HandleFunc("/api/nodes/probe-all", s.withAuth(s.handleProbeAll))
	mux.HandleFunc("/api/nodes/traffic/stream", s.withAuth(s.handleTrafficStream))
	mux.HandleFunc("/api/nodes/", s.withAuth(s.handleNodeAction))
	mux.HandleFunc("/api/debug", s.withAuth(s.handleDebug))
	mux.HandleFunc("/api/export", s.withAuth(s.handleExport))
	mux.HandleFunc("/api/import", s.withAuth(s.handleImport))
	mux.HandleFunc("/api/subscription/status", s.withAuth(s.handleSubscriptionStatus))
	mux.HandleFunc("/api/subscription/refresh", s.withAuth(s.handleSubscriptionRefresh))
	mux.HandleFunc("/api/subscriptions", s.withAuth(s.handleSubscriptions))
	mux.HandleFunc("/api/subscriptions/", s.withAuth(s.handleSubscriptionItem))
	mux.HandleFunc("/api/geoip/refresh", s.withAuth(s.handleGeoIPRefresh))
	mux.HandleFunc("/api/reload", s.withAuth(s.handleReload))
	mux.HandleFunc("/api/traffic", s.withAuth(s.handleTraffic))
	mux.HandleFunc("/api/logs", s.withAuth(s.handleLogs))
	s.srv = &http.Server{Addr: cfg.Listen, Handler: mux}
	return s
}

// SetSubscriptionRefresher sets the subscription refresher for API endpoints.
func (s *Server) SetSubscriptionRefresher(sr SubscriptionRefresher) {
	if s != nil {
		s.subRefresher = sr
	}
}

// SetNodeManager enables config-node CRUD endpoints.
func (s *Server) SetNodeManager(nm NodeManager) {
	if s != nil {
		s.nodeMgr = nm
	}
}

// SetStore enables SQLite-backed settings and session persistence.
func (s *Server) SetStore(st store.Store) {
	if s != nil {
		s.store = st
	}
}

// SetConfig binds the persistable config object for settings API.
func (s *Server) SetConfig(cfg *config.Config) {
	if s == nil {
		return
	}
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	s.cfgSrc = cfg
	if cfg != nil {
		s.cfg.ExternalIP = cfg.ExternalIP
		s.cfg.ProbeTarget = cfg.Management.ProbeTarget
		s.cfg.SkipCertVerify = cfg.SkipCertVerify
		// Sync proxy credentials based on mode
		if cfg.Mode == "multi-port" || cfg.Mode == "hybrid" {
			s.cfg.ProxyUsername = cfg.MultiPort.Username
			s.cfg.ProxyPassword = cfg.MultiPort.Password
		} else {
			s.cfg.ProxyUsername = cfg.Listener.Username
			s.cfg.ProxyPassword = cfg.Listener.Password
		}
	}
}

// getSettings returns current dynamic settings (thread-safe).
func (s *Server) getSettings() (externalIP, probeTarget string, skipCertVerify bool, logCfg config.LogConfig) {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	logCfg = config.LogConfig{}
	if s.cfgSrc != nil {
		logCfg = s.cfgSrc.Log
	}
	return s.cfg.ExternalIP, s.cfg.ProbeTarget, s.cfg.SkipCertVerify, logCfg
}

// updateSettings updates dynamic settings in memory. Persistence is handled by the caller.
func (s *Server) updateSettings(externalIP, probeTarget string, skipCertVerify bool, logCfg *config.LogConfig, geoipEnabled bool) error {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()

	s.cfg.ExternalIP = externalIP
	s.cfg.ProbeTarget = probeTarget
	s.cfg.SkipCertVerify = skipCertVerify

	if s.cfgSrc == nil {
		return errors.New("配置存储未初始化")
	}

	s.cfgSrc.ExternalIP = externalIP
	s.cfgSrc.Management.ProbeTarget = probeTarget
	s.cfgSrc.SkipCertVerify = skipCertVerify

	s.cfgSrc.GeoIP.Enabled = geoipEnabled
	if geoipEnabled {
		s.cfgSrc.GeoIP.DatabasePath = config.DefaultGeoIPDatabasePath()
	}
	if s.cfgSrc.GeoIP.AutoUpdateInterval <= 0 {
		s.cfgSrc.GeoIP.AutoUpdateInterval = 24 * time.Hour
	}

	if logCfg != nil {
		s.cfgSrc.Log.Output = logCfg.Output
		if logCfg.MaxSize > 0 {
			s.cfgSrc.Log.MaxSize = logCfg.MaxSize
		}
		if logCfg.MaxBackups > 0 {
			s.cfgSrc.Log.MaxBackups = logCfg.MaxBackups
		}
		if logCfg.MaxAge > 0 {
			s.cfgSrc.Log.MaxAge = logCfg.MaxAge
		}
		s.cfgSrc.Log.Compress = logCfg.Compress
	}

	return nil
}

// Start launches the HTTP server.
func (s *Server) Start(ctx context.Context) {
	if s == nil || s.srv == nil {
		return
	}
	s.logger.Printf("Starting monitor server on %s", s.cfg.Listen)
	go func() {
		if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Printf("❌ Monitor server error: %v", err)
		}
	}()
	// Give server a moment to start and check for immediate errors
	time.Sleep(100 * time.Millisecond)
	s.logger.Printf("✅ Monitor server started on http://%s", s.cfg.Listen)

	go func() {
		<-ctx.Done()
		s.Shutdown(context.Background())
	}()
}

// Shutdown stops the server gracefully.
func (s *Server) Shutdown(ctx context.Context) {
	if s == nil || s.srv == nil {
		return
	}
	_ = s.srv.Shutdown(ctx)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	if r.URL.Path != "/" && r.URL.Path != "/index.html" {
		cleanPath := "assets" + r.URL.Path
		if file, err := embeddedFS.Open(cleanPath); err == nil {
			_ = file.Close()
			r.URL.Path = cleanPath
			http.FileServer(http.FS(embeddedFS)).ServeHTTP(w, r)
			return
		}
	}

	data, err := embeddedFS.ReadFile("assets/index.html")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	allNodes := s.mgr.Snapshot()
	totalNodes := len(allNodes)

	// Calculate region statistics
	regionStats := make(map[string]int)
	regionHealthy := make(map[string]int)
	for _, snap := range allNodes {
		region := snap.Region
		if region == "" {
			region = "other"
		}
		regionStats[region]++
		// Count healthy nodes per region
		if snap.InitialCheckDone && snap.Available && !snap.Blacklisted {
			regionHealthy[region]++
		}
	}
	traffic := s.mgr.TrafficSummary(false)

	payload := map[string]any{
		"nodes":           allNodes,
		"total_nodes":     totalNodes,
		"total_upload":    traffic.TotalUpload,
		"total_download":  traffic.TotalDownload,
		"upload_speed":    traffic.UploadSpeed,
		"download_speed":  traffic.DownloadSpeed,
		"traffic_sampled": traffic.SampledAt,
		"region_stats":    regionStats,
		"region_healthy":  regionHealthy,
	}
	writeJSON(w, payload)
}

func (s *Server) handleDebug(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	snapshots := s.mgr.Snapshot()
	var totalCalls, totalSuccess int64
	debugNodes := make([]map[string]any, 0, len(snapshots))
	for _, snap := range snapshots {
		totalCalls += snap.SuccessCount + int64(snap.FailureCount)
		totalSuccess += snap.SuccessCount
		debugNodes = append(debugNodes, map[string]any{
			"tag":                snap.Tag,
			"name":               snap.Name,
			"mode":               snap.Mode,
			"port":               snap.Port,
			"failure_count":      snap.FailureCount,
			"success_count":      snap.SuccessCount,
			"active_connections": snap.ActiveConnections,
			"last_latency_ms":    snap.LastLatencyMs,
			"last_success":       snap.LastSuccess,
			"last_failure":       snap.LastFailure,
			"last_error":         snap.LastError,
			"blacklisted":        snap.Blacklisted,
			"total_upload":       snap.TotalUpload,
			"total_download":     snap.TotalDownload,
			"upload_speed":       snap.UploadSpeed,
			"download_speed":     snap.DownloadSpeed,
			"timeline":           snap.Timeline,
		})
	}
	var successRate float64
	if totalCalls > 0 {
		successRate = float64(totalSuccess) / float64(totalCalls) * 100
	}
	writeJSON(w, map[string]any{
		"nodes":         debugNodes,
		"total_calls":   totalCalls,
		"total_success": totalSuccess,
		"success_rate":  successRate,
	})
}

func (s *Server) handleNodeAction(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/nodes/"), "/")
	if len(parts) < 1 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	tag := parts[0]
	if tag == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	switch action {
	case "probe":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		latency, err := s.mgr.Probe(ctx, tag)
		if err != nil {
			writeJSON(w, map[string]any{"error": err.Error()})
			return
		}
		latencyMs := latency.Milliseconds()
		if latencyMs == 0 && latency > 0 {
			latencyMs = 1 // Round up sub-millisecond latencies to 1ms
		}
		writeJSON(w, map[string]any{"message": "探测成功", "latency_ms": latencyMs})
	case "release":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if err := s.mgr.Release(tag); err != nil {
			writeJSON(w, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, map[string]any{"message": "已解除拉黑"})
	case "blacklist":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Duration string `json:"duration"` // e.g. "1h", "24h", "30m"
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Duration == "" {
			req.Duration = "24h"
		}
		duration, err := time.ParseDuration(req.Duration)
		if err != nil || duration <= 0 {
			duration = 24 * time.Hour
		}
		if err := s.mgr.ManualBlacklist(tag, duration); err != nil {
			writeJSON(w, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, map[string]any{"message": fmt.Sprintf("已拉黑 %s", duration)})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// handleProbeAll probes all nodes in batches and returns results via SSE
func (s *Server) handleProbeAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	// Get all nodes
	snapshots := s.mgr.Snapshot()
	total := len(snapshots)
	if total == 0 {
		emptyData, _ := json.Marshal(map[string]any{"type": "complete", "total": 0, "success": 0, "failed": 0})
		fmt.Fprintf(w, "data: %s\n\n", emptyData)
		flusher.Flush()
		return
	}

	// Send start event
	startData, _ := json.Marshal(map[string]any{"type": "start", "total": total})
	fmt.Fprintf(w, "data: %s\n\n", startData)
	flusher.Flush()

	// Create context with timeout
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()

	// Probe all nodes with semaphore control
	type probeResult struct {
		tag     string
		name    string
		latency int64
		err     string
	}
	results := make(chan probeResult, total)
	var wg sync.WaitGroup

	// Launch probes with semaphore control
	for _, snap := range snapshots {
		wg.Add(1)
		go func(snap Snapshot) {
			defer wg.Done()

			// Acquire semaphore permit
			if err := s.probeSem.Acquire(ctx, 1); err != nil {
				results <- probeResult{
					tag:  snap.Tag,
					name: snap.Name,
					err:  "probe cancelled: " + err.Error(),
				}
				return
			}
			defer s.probeSem.Release(1)

			// Execute probe
			probeCtx, probeCancel := context.WithTimeout(ctx, 10*time.Second)
			defer probeCancel()

			latency, err := s.mgr.Probe(probeCtx, snap.Tag)
			if err != nil {
				results <- probeResult{
					tag:     snap.Tag,
					name:    snap.Name,
					latency: -1,
					err:     err.Error(),
				}
			} else {
				results <- probeResult{
					tag:     snap.Tag,
					name:    snap.Name,
					latency: latency.Milliseconds(),
					err:     "",
				}
			}
		}(snap)
	}

	// Wait for all probes to complete
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results
	successCount := 0
	failedCount := 0
	count := 0

	for result := range results {
		count++
		if result.err != "" {
			failedCount++
		} else {
			successCount++
		}

		status := "success"
		if result.err != "" {
			status = "error"
		}

		eventPayload := map[string]any{
			"type":     "progress",
			"tag":      result.tag,
			"name":     result.name,
			"latency":  result.latency,
			"status":   status,
			"error":    result.err,
			"current":  count,
			"total":    total,
			"progress": float64(count) / float64(total) * 100,
		}
		eventData, _ := json.Marshal(eventPayload)
		fmt.Fprintf(w, "data: %s\n\n", eventData)
		flusher.Flush()
	}

	// Send complete event
	completeData, _ := json.Marshal(map[string]any{"type": "complete", "total": total, "success": successCount, "failed": failedCount})
	fmt.Fprintf(w, "data: %s\n\n", completeData)
	flusher.Flush()
}

func writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

func parseDurationOr(value string, fallback time.Duration) time.Duration {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

// withAuth 认证中间件，如果配置了密码则需要验证
func (s *Server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 如果没有配置密码，直接放行
		if s.cfg.Password == "" {
			next(w, r)
			return
		}

		if s.validateSessionFromRequest(r) {
			next(w, r)
			return
		}

		// 未授权
		w.WriteHeader(http.StatusUnauthorized)
		writeJSON(w, map[string]any{"error": "未授权，请先登录"})
	}
}

func (s *Server) validateSessionFromRequest(r *http.Request) bool {
	cookie, err := r.Cookie("session_token")
	if err == nil && s.validateSession(cookie.Value) {
		return true
	}

	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return false
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")
	return s.validateSession(token)
}

// handleAuth 处理登录认证
func (s *Server) handleAuth(w http.ResponseWriter, r *http.Request) {
	// 如果没有配置密码，直接返回成功（不需要token）
	if s.cfg.Password == "" {
		writeJSON(w, map[string]any{"message": "无需密码", "no_password": true})
		return
	}

	if r.Method == http.MethodGet {
		if s.validateSessionFromRequest(r) {
			writeJSON(w, map[string]any{"message": "已登录"})
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		writeJSON(w, map[string]any{"error": "未授权，请先登录"})
		return
	}

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Password string `json:"password"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]any{"error": "请求格式错误"})
		return
	}

	// 使用 constant-time 比较防止时序攻击
	if !secureCompareStrings(req.Password, s.cfg.Password) {
		// 添加随机延迟防止暴力破解
		time.Sleep(time.Duration(100+mathrand.Intn(200)) * time.Millisecond)
		w.WriteHeader(http.StatusUnauthorized)
		writeJSON(w, map[string]any{"error": "密码错误"})
		return
	}

	// 创建新会话
	session, err := s.createSession()
	if err != nil {
		s.logger.Printf("Failed to create session: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		writeJSON(w, map[string]any{"error": "服务器错误"})
		return
	}

	// 设置 HttpOnly Cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "session_token",
		Value:    session.Token,
		Path:     "/",
		HttpOnly: true,
		Secure:   false, // 生产环境应启用 HTTPS 并设为 true
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(s.sessionTTL.Seconds()),
	})

	writeJSON(w, map[string]any{
		"message": "登录成功",
		"token":   session.Token,
	})
}

// handleExport 导出所有可用代理池节点的代理 URI，每行一个。
// query 参数:
//   - scheme=http   (默认)
//   - scheme=socks5
//   - scheme=all    (同时导出 HTTP 和 SOCKS5)
//
// 在 pool/hybrid 模式下，还会导出 Pool 代理池入口和 GeoIP 分区路由入口。
// 导出内容会遵循 listener.protocol 与 multi_port.protocol。
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	scheme := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("scheme")))
	if scheme == "" {
		scheme = "http"
	}
	if scheme != "http" && scheme != "socks5" && scheme != "all" {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]any{"error": "invalid scheme, use http/socks5/all"})
		return
	}

	// 只导出初始检查通过的可用节点
	snapshots := s.mgr.SnapshotFiltered(true)
	var lines []string

	seen := make(map[string]bool)

	// 读取运行模式和监听配置
	s.cfgMu.RLock()
	mode := ""
	var listenerCfg config.ListenerConfig
	var multiPortCfg config.MultiPortConfig
	var geoipCfg config.GeoIPConfig
	if s.cfgSrc != nil {
		mode = s.cfgSrc.Mode
		listenerCfg = s.cfgSrc.Listener
		multiPortCfg = s.cfgSrc.MultiPort
		geoipCfg = s.cfgSrc.GeoIP
	}
	s.cfgMu.RUnlock()

	// Pool 代理池入口（pool 或 hybrid 模式）
	if (mode == "pool" || mode == "hybrid") && listenerCfg.Port > 0 {
		poolAddr := listenerCfg.Address
		if poolAddr == "" || poolAddr == "0.0.0.0" || poolAddr == "::" {
			if extIP, _, _, _ := s.getSettings(); extIP != "" {
				poolAddr = extIP
			}
		}
		var poolAuth string
		if listenerCfg.Username != "" && listenerCfg.Password != "" {
			poolAuth = fmt.Sprintf("%s:%s@", listenerCfg.Username, listenerCfg.Password)
		}
		poolURIs := exportProxyURIsForProtocol(listenerCfg.Protocol, scheme, poolAuth, poolAddr, listenerCfg.Port)
		if len(poolURIs) > 0 {
			lines = append(lines, "# Pool 代理池入口")
			for _, uri := range poolURIs {
				if !seen[uri] {
					lines = append(lines, uri)
					seen[uri] = true
				}
			}
		}
	}

	// GeoIP 分区路由入口
	if geoipCfg.Enabled && geoipCfg.Port > 0 {
		geoAddr := geoipCfg.Listen
		if geoAddr == "" || geoAddr == "0.0.0.0" || geoAddr == "::" {
			if extIP, _, _, _ := s.getSettings(); extIP != "" {
				geoAddr = extIP
			}
		}
		var geoAuth string
		if listenerCfg.Username != "" && listenerCfg.Password != "" {
			geoAuth = fmt.Sprintf("%s:%s@", listenerCfg.Username, listenerCfg.Password)
		}
		regions := geoip.AllRegions()
		var pathParts []string
		for _, r := range regions {
			if r != "other" {
				pathParts = append(pathParts, fmt.Sprintf("/%s/", r))
			}
		}
		lines = append(lines, fmt.Sprintf("# GeoIP 分区路由入口 (支持路径: %s)", strings.Join(pathParts, " ")))
		// GeoIP 路由仅支持 HTTP
		geoURI := fmt.Sprintf("http://%s%s:%d", geoAuth, geoAddr, geoipCfg.Port)
		if !seen[geoURI] {
			lines = append(lines, geoURI)
			seen[geoURI] = true
		}
	}

	// Multi-port 独立节点
	multiPortHeaderAdded := false
	if len(snapshots) > 0 && (mode == "hybrid" || mode == "multi-port" || mode == "") {
		for _, snap := range snapshots {
			// 只导出有监听地址和端口的节点
			if snap.ListenAddress == "" || snap.Port == 0 {
				continue
			}

			listenAddr := snap.ListenAddress
			if listenAddr == "0.0.0.0" || listenAddr == "::" {
				if extIP, _, _, _ := s.getSettings(); extIP != "" {
					listenAddr = extIP
				}
			}

			var authPart string
			if multiPortCfg.Username != "" && multiPortCfg.Password != "" {
				authPart = fmt.Sprintf("%s:%s@", multiPortCfg.Username, multiPortCfg.Password)
			}
			uris := exportProxyURIsForProtocol(multiPortCfg.Protocol, scheme, authPart, listenAddr, snap.Port)
			for _, uri := range uris {
				if !seen[uri] {
					if !multiPortHeaderAdded {
						lines = append(lines, "# Multi-port 独立节点")
						multiPortHeaderAdded = true
					}
					lines = append(lines, uri)
					seen[uri] = true
				}
			}
		}
	}

	// 返回纯文本，每行一个 URI
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	filename := "proxy_pool.txt"
	if scheme == "socks5" {
		filename = "proxy_pool_socks5.txt"
	} else if scheme == "all" {
		filename = "proxy_pool_all.txt"
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filename))
	_, _ = w.Write([]byte(strings.Join(lines, "\n")))
}

func exportProxyURIsForProtocol(protocol, scheme, auth, address string, port uint16) []string {
	normalized, err := config.NormalizeInboundProtocol(protocol)
	if err != nil {
		normalized = config.InboundProtocolMixed
	}

	httpURI := fmt.Sprintf("http://%s%s:%d", auth, address, port)
	socksURI := fmt.Sprintf("socks5://%s%s:%d", auth, address, port)
	switch normalized {
	case config.InboundProtocolHTTP:
		if scheme == "http" || scheme == "all" {
			return []string{httpURI}
		}
	case config.InboundProtocolSOCKS5:
		if scheme == "socks5" || scheme == "all" {
			return []string{socksURI}
		}
	default:
		switch scheme {
		case "http":
			return []string{httpURI}
		case "socks5":
			return []string{socksURI}
		case "all":
			return []string{httpURI, socksURI}
		}
	}
	return nil
}

// handleImport imports proxy URI lines into config nodes.
func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !s.ensureNodeManager(w) {
		return
	}

	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]any{"error": "请求格式错误"})
		return
	}

	content := strings.TrimSpace(req.Content)
	if content == "" {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]any{"error": "导入内容为空"})
		return
	}

	var imported int
	var errs []string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !config.IsProxyURI(line) {
			errs = append(errs, fmt.Sprintf("无效的代理 URI: %s", truncateString(line, 80)))
			continue
		}

		name := config.ExtractNodeName(line)
		if name == "" {
			name = fmt.Sprintf("imported-%d", imported+1)
		}
		if _, err := s.nodeMgr.CreateNode(r.Context(), config.NodeConfig{Name: name, URI: line}); err != nil {
			errs = append(errs, fmt.Sprintf("添加节点 %q 失败: %v", name, err))
			continue
		}
		imported++
	}

	result := map[string]any{
		"message":     fmt.Sprintf("成功导入 %d 个节点，请点击重载使配置生效", imported),
		"imported":    imported,
		"need_reload": imported > 0,
	}
	if len(errs) > 0 {
		result["errors"] = errs
	}
	writeJSON(w, result)
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// handleSettings handles GET/PUT for dynamic settings (external_ip, probe_target, skip_cert_verify, log).
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		extIP, probeTarget, skipCertVerify, logCfg := s.getSettings()

		// Read full config for extended fields
		s.cfgMu.RLock()
		cfg := s.cfgSrc
		s.cfgMu.RUnlock()

		resp := map[string]any{
			"external_ip":      extIP,
			"probe_target":     probeTarget,
			"log_level":        "",
			"skip_cert_verify": skipCertVerify,
			"log": map[string]any{
				"output":      logCfg.Output,
				"file":        logCfg.File,
				"max_size":    logCfg.MaxSize,
				"max_backups": logCfg.MaxBackups,
				"max_age":     logCfg.MaxAge,
				"compress":    logCfg.Compress,
			},
			"geoip": map[string]any{
				"enabled":              false,
				"listen":               "",
				"port":                 0,
				"auto_update_enabled":  false,
				"auto_update_interval": "",
			},
		}
		if cfg != nil {
			resp["mode"] = cfg.Mode
			resp["log_level"] = cfg.LogLevel
			resp["listener"] = map[string]any{
				"address":  cfg.Listener.Address,
				"port":     cfg.Listener.Port,
				"protocol": cfg.Listener.Protocol,
				"username": cfg.Listener.Username,
				"password": cfg.Listener.Password,
			}
			resp["multi_port"] = map[string]any{
				"address":   cfg.MultiPort.Address,
				"base_port": cfg.MultiPort.BasePort,
				"protocol":  cfg.MultiPort.Protocol,
				"username":  cfg.MultiPort.Username,
				"password":  cfg.MultiPort.Password,
			}
			resp["pool"] = map[string]any{
				"mode":               cfg.Pool.Mode,
				"failure_threshold":  cfg.Pool.FailureThreshold,
				"blacklist_duration": cfg.Pool.BlacklistDuration.String(),
			}
			resp["management"] = map[string]any{
				"enabled":      cfg.ManagementEnabled(),
				"listen":       cfg.Management.Listen,
				"probe_target": cfg.Management.ProbeTarget,
			}
			resp["geoip"] = map[string]any{
				"enabled":              cfg.GeoIP.Enabled,
				"listen":               cfg.GeoIP.Listen,
				"port":                 cfg.GeoIP.Port,
				"auto_update_enabled":  cfg.GeoIP.AutoUpdateEnabled,
				"auto_update_interval": cfg.GeoIP.AutoUpdateInterval.String(),
			}
		}
		writeJSON(w, resp)
	case http.MethodPut:
		var req struct {
			ExternalIP     string `json:"external_ip"`
			ProbeTarget    string `json:"probe_target"`
			LogLevel       string `json:"log_level,omitempty"`
			SkipCertVerify bool   `json:"skip_cert_verify"`
			Mode           string `json:"mode,omitempty"`
			Listener       *struct {
				Address  string `json:"address"`
				Port     uint16 `json:"port"`
				Protocol string `json:"protocol"`
				Username string `json:"username"`
				Password string `json:"password"`
			} `json:"listener,omitempty"`
			MultiPort *struct {
				Address  string `json:"address"`
				BasePort uint16 `json:"base_port"`
				Protocol string `json:"protocol"`
				Username string `json:"username"`
				Password string `json:"password"`
			} `json:"multi_port,omitempty"`
			Pool *struct {
				Mode              string `json:"mode"`
				FailureThreshold  int    `json:"failure_threshold"`
				BlacklistDuration string `json:"blacklist_duration"`
			} `json:"pool,omitempty"`
			Management *struct {
				Enabled     *bool  `json:"enabled,omitempty"`
				Listen      string `json:"listen"`
				ProbeTarget string `json:"probe_target"`
			} `json:"management,omitempty"`
			Log *struct {
				Output     string `json:"output"`
				MaxSize    int    `json:"max_size"`
				MaxBackups int    `json:"max_backups"`
				MaxAge     int    `json:"max_age"`
				Compress   bool   `json:"compress"`
			} `json:"log"`
			GeoIP *struct {
				Enabled            bool   `json:"enabled"`
				Listen             string `json:"listen"`
				Port               uint16 `json:"port"`
				AutoUpdateEnabled  bool   `json:"auto_update_enabled"`
				AutoUpdateInterval string `json:"auto_update_interval"`
			} `json:"geoip"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			writeJSON(w, map[string]any{"error": "请求格式错误"})
			return
		}

		extIP := strings.TrimSpace(req.ExternalIP)
		probeTarget := strings.TrimSpace(req.ProbeTarget)

		var logCfg *config.LogConfig
		if req.Log != nil {
			logCfg = &config.LogConfig{
				Output:     req.Log.Output,
				MaxSize:    req.Log.MaxSize,
				MaxBackups: req.Log.MaxBackups,
				MaxAge:     req.Log.MaxAge,
				Compress:   req.Log.Compress,
			}
		}

		var listenerProtocol string
		if req.Listener != nil && req.Listener.Protocol != "" {
			normalized, err := config.NormalizeInboundProtocol(req.Listener.Protocol)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				writeJSON(w, map[string]any{"error": err.Error()})
				return
			}
			listenerProtocol = normalized
		}
		var multiPortProtocol string
		if req.MultiPort != nil && req.MultiPort.Protocol != "" {
			normalized, err := config.NormalizeInboundProtocol(req.MultiPort.Protocol)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				writeJSON(w, map[string]any{"error": err.Error()})
				return
			}
			multiPortProtocol = normalized
		}

		if err := s.updateSettings(extIP, probeTarget, req.SkipCertVerify, logCfg, req.GeoIP != nil && req.GeoIP.Enabled); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			writeJSON(w, map[string]any{"error": err.Error()})
			return
		}

		// Update extended settings
		s.cfgMu.Lock()
		if s.cfgSrc != nil {
			if req.Mode != "" {
				s.cfgSrc.Mode = req.Mode
			}
			if req.LogLevel != "" {
				s.cfgSrc.LogLevel = req.LogLevel
			}
			if req.Listener != nil {
				s.cfgSrc.Listener.Address = req.Listener.Address
				s.cfgSrc.Listener.Port = req.Listener.Port
				if listenerProtocol != "" {
					s.cfgSrc.Listener.Protocol = listenerProtocol
				}
				s.cfgSrc.Listener.Username = req.Listener.Username
				s.cfgSrc.Listener.Password = req.Listener.Password
			}
			if req.MultiPort != nil {
				s.cfgSrc.MultiPort.Address = req.MultiPort.Address
				s.cfgSrc.MultiPort.BasePort = req.MultiPort.BasePort
				if multiPortProtocol != "" {
					s.cfgSrc.MultiPort.Protocol = multiPortProtocol
				}
				s.cfgSrc.MultiPort.Username = req.MultiPort.Username
				s.cfgSrc.MultiPort.Password = req.MultiPort.Password
			}
			if req.Pool != nil {
				s.cfgSrc.Pool.Mode = req.Pool.Mode
				s.cfgSrc.Pool.FailureThreshold = req.Pool.FailureThreshold
				if req.Pool.BlacklistDuration != "" {
					if d, err := time.ParseDuration(req.Pool.BlacklistDuration); err == nil {
						s.cfgSrc.Pool.BlacklistDuration = d
					}
				}
			}
			if req.Management != nil {
				if req.Management.Enabled != nil {
					s.cfgSrc.Management.Enabled = req.Management.Enabled
				}
				s.cfgSrc.Management.Listen = req.Management.Listen
				if req.Management.ProbeTarget != "" {
					s.cfgSrc.Management.ProbeTarget = req.Management.ProbeTarget
				}
			}
			if req.GeoIP != nil {
				s.cfgSrc.GeoIP.DatabasePath = config.DefaultGeoIPDatabasePath()
				s.cfgSrc.GeoIP.Listen = req.GeoIP.Listen
				s.cfgSrc.GeoIP.Port = req.GeoIP.Port
				s.cfgSrc.GeoIP.AutoUpdateEnabled = req.GeoIP.AutoUpdateEnabled
				if req.GeoIP.AutoUpdateInterval != "" {
					if d, err := time.ParseDuration(req.GeoIP.AutoUpdateInterval); err == nil {
						s.cfgSrc.GeoIP.AutoUpdateInterval = d
					}
				}
			}
			if s.store != nil {
				if err := config.SaveRuntime(r.Context(), s.store, s.cfgSrc); err != nil {
					s.cfgMu.Unlock()
					w.WriteHeader(http.StatusInternalServerError)
					writeJSON(w, map[string]any{"error": fmt.Sprintf("保存配置失败: %v", err)})
					return
				}
			}
		}
		s.cfgMu.Unlock()

		writeJSON(w, map[string]any{
			"message":          "设置已保存",
			"external_ip":      extIP,
			"probe_target":     probeTarget,
			"skip_cert_verify": req.SkipCertVerify,
			"need_reload":      true,
		})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleSubscriptionStatus returns the current subscription refresh status.
func (s *Server) handleSubscriptionStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	if s.subRefresher == nil {
		writeJSON(w, map[string]any{
			"enabled": false,
			"message": "订阅刷新未启用",
		})
		return
	}

	status := s.subRefresher.Status()
	hasSubscriptions := false
	if s.store != nil {
		if sources, err := s.store.ListSubscriptionSources(r.Context()); err == nil {
			for _, source := range sources {
				if source.Enabled && source.URL != "" {
					hasSubscriptions = true
					break
				}
			}
		}
	}
	writeJSON(w, map[string]any{
		"enabled":           true,
		"has_subscriptions": hasSubscriptions,
		"last_refresh":      status.LastRefresh,
		"next_refresh":      status.NextRefresh,
		"node_count":        status.NodeCount,
		"last_error":        status.LastError,
		"refresh_count":     status.RefreshCount,
		"is_refreshing":     status.IsRefreshing,
	})
}

// handleSubscriptionRefresh triggers an immediate subscription refresh.
func (s *Server) handleSubscriptionRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	if s.subRefresher == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		writeJSON(w, map[string]any{"error": "订阅刷新未启用"})
		return
	}

	if err := s.subRefresher.RefreshNow(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		writeJSON(w, map[string]any{"error": err.Error()})
		return
	}

	status := s.subRefresher.Status()
	writeJSON(w, map[string]any{
		"message":    "刷新成功",
		"node_count": status.NodeCount,
	})
}

type subscriptionSourcePayload struct {
	Name       string `json:"name"`
	URL        string `json:"url"`
	Enabled    *bool  `json:"enabled,omitempty"`
	AutoUpdate *bool  `json:"auto_update,omitempty"`
	Interval   string `json:"interval"`
}

func (s *Server) handleSubscriptions(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		w.WriteHeader(http.StatusInternalServerError)
		writeJSON(w, map[string]any{"error": "数据库存储未初始化"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		sources, err := s.store.ListSubscriptionSources(r.Context())
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			writeJSON(w, map[string]any{"error": fmt.Sprintf("读取订阅失败: %v", err)})
			return
		}
		resp := make([]map[string]any, 0, len(sources))
		for _, source := range sources {
			resp = append(resp, subscriptionSourceResponse(source))
		}
		writeJSON(w, map[string]any{"subscriptions": resp})

	case http.MethodPost:
		var payload subscriptionSourcePayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			writeJSON(w, map[string]any{"error": "请求格式错误"})
			return
		}
		source, err := sourceFromPayload(payload, nil)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			writeJSON(w, map[string]any{"error": err.Error()})
			return
		}
		if err := s.store.CreateSubscriptionSource(r.Context(), &source); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			writeJSON(w, map[string]any{"error": fmt.Sprintf("保存订阅失败: %v", err)})
			return
		}
		if s.subRefresher != nil {
			s.subRefresher.ReloadSchedule()
		}
		writeJSON(w, map[string]any{"subscription": subscriptionSourceResponse(source), "message": "订阅已添加"})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleSubscriptionItem(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		w.WriteHeader(http.StatusInternalServerError)
		writeJSON(w, map[string]any{"error": "数据库存储未初始化"})
		return
	}

	id, action, ok := parseSubscriptionItemPath(r.URL.Path)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		writeJSON(w, map[string]any{"error": "订阅不存在"})
		return
	}

	switch {
	case action == "refresh" && r.Method == http.MethodPost:
		if s.subRefresher == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			writeJSON(w, map[string]any{"error": "订阅刷新未启用"})
			return
		}
		if err := s.subRefresher.RefreshSource(id); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			writeJSON(w, map[string]any{"error": err.Error()})
			return
		}
		source, _ := s.store.GetSubscriptionSource(r.Context(), id)
		resp := map[string]any{"message": "订阅刷新成功"}
		if source != nil {
			resp["subscription"] = subscriptionSourceResponse(*source)
		}
		writeJSON(w, resp)

	case action == "" && r.Method == http.MethodPut:
		current, err := s.store.GetSubscriptionSource(r.Context(), id)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			writeJSON(w, map[string]any{"error": fmt.Sprintf("读取订阅失败: %v", err)})
			return
		}
		if current == nil {
			w.WriteHeader(http.StatusNotFound)
			writeJSON(w, map[string]any{"error": "订阅不存在"})
			return
		}
		var payload subscriptionSourcePayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			writeJSON(w, map[string]any{"error": "请求格式错误"})
			return
		}
		next, err := sourceFromPayload(payload, current)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			writeJSON(w, map[string]any{"error": err.Error()})
			return
		}
		if err := s.store.UpdateSubscriptionSource(r.Context(), &next); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			writeJSON(w, map[string]any{"error": fmt.Sprintf("保存订阅失败: %v", err)})
			return
		}
		if current.URL != next.URL {
			_ = s.deleteSubscriptionNodes(r.Context(), id)
		}
		if s.subRefresher != nil {
			s.subRefresher.ReloadSchedule()
		}
		writeJSON(w, map[string]any{"subscription": subscriptionSourceResponse(next), "message": "订阅已保存"})

	case action == "" && r.Method == http.MethodDelete:
		if err := s.store.DeleteSubscriptionSource(r.Context(), id); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			writeJSON(w, map[string]any{"error": fmt.Sprintf("删除订阅失败: %v", err)})
			return
		}
		if err := s.deleteSubscriptionNodes(r.Context(), id); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			writeJSON(w, map[string]any{"error": fmt.Sprintf("删除订阅节点失败: %v", err)})
			return
		}
		if s.subRefresher != nil {
			s.subRefresher.ReloadSchedule()
		}
		if s.nodeMgr != nil {
			_ = s.nodeMgr.TriggerReload(r.Context())
		}
		writeJSON(w, map[string]any{"message": "订阅已删除"})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func parseSubscriptionItemPath(path string) (int64, string, bool) {
	rest := strings.TrimPrefix(path, "/api/subscriptions/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return 0, "", false
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id <= 0 {
		return 0, "", false
	}
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	return id, action, len(parts) <= 2
}

func sourceFromPayload(payload subscriptionSourcePayload, current *store.SubscriptionSource) (store.SubscriptionSource, error) {
	source := store.SubscriptionSource{Enabled: true, AutoUpdate: true, Interval: time.Hour}
	changed := current == nil
	if current != nil {
		source = *current
	}
	previousURL := source.URL
	previousEnabled := source.Enabled
	previousAutoUpdate := source.AutoUpdate
	previousInterval := source.Interval

	source.Name = strings.TrimSpace(payload.Name)
	source.URL = strings.TrimSpace(payload.URL)
	if source.URL == "" {
		return source, errors.New("订阅 URL 不能为空")
	}
	if payload.Enabled != nil {
		source.Enabled = *payload.Enabled
	}
	if payload.AutoUpdate != nil {
		source.AutoUpdate = *payload.AutoUpdate
	}
	if strings.TrimSpace(payload.Interval) != "" {
		interval, err := time.ParseDuration(payload.Interval)
		if err != nil || interval < time.Minute {
			return source, errors.New("刷新间隔格式错误，最小 1 分钟")
		}
		source.Interval = interval
	}
	changed = changed ||
		previousURL != source.URL ||
		previousEnabled != source.Enabled ||
		previousAutoUpdate != source.AutoUpdate ||
		previousInterval != source.Interval
	if !source.Enabled || !source.AutoUpdate {
		source.NextRefresh = time.Time{}
	} else if changed {
		source.NextRefresh = time.Now()
	}
	return source, nil
}

func subscriptionSourceResponse(source store.SubscriptionSource) map[string]any {
	return map[string]any{
		"id":           source.ID,
		"name":         source.Name,
		"url":          source.URL,
		"enabled":      source.Enabled,
		"auto_update":  source.AutoUpdate,
		"interval":     source.Interval.String(),
		"last_refresh": source.LastRefresh,
		"next_refresh": source.NextRefresh,
		"node_count":   source.NodeCount,
		"last_error":   source.LastError,
		"created_at":   source.CreatedAt,
		"updated_at":   source.UpdatedAt,
	}
}

func (s *Server) deleteSubscriptionNodes(ctx context.Context, sourceID int64) error {
	nodes, err := s.store.ListNodes(ctx, store.NodeFilter{Source: store.NodeSourceSubscription, SubscriptionID: sourceID})
	if err != nil {
		return err
	}
	for _, node := range nodes {
		if err := s.store.DeleteNode(ctx, node.ID); err != nil {
			return err
		}
	}
	return nil
}

// nodePayload is the JSON request body for node CRUD operations.
type nodePayload struct {
	Name            string `json:"name"`
	URI             string `json:"uri"`
	OutboundJSON    string `json:"outbound_json"`
	Port            uint16 `json:"port"`
	InboundProtocol string `json:"inbound_protocol"`
	Username        string `json:"username"`
	Password        string `json:"password"`
	Disabled        bool   `json:"disabled"`
}

func (p nodePayload) toConfig() config.NodeConfig {
	return config.NodeConfig{
		Name:            p.Name,
		URI:             p.URI,
		OutboundJSON:    p.OutboundJSON,
		Port:            p.Port,
		InboundProtocol: p.InboundProtocol,
		Username:        p.Username,
		Password:        p.Password,
		Disabled:        p.Disabled,
	}
}

func (s *Server) handleNodeURIParse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !s.ensureNodeManager(w) {
		return
	}

	var payload struct {
		Name string `json:"name"`
		URI  string `json:"uri"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]any{"error": "请求格式错误"})
		return
	}

	node, err := s.nodeMgr.ParseNodeURI(r.Context(), payload.Name, payload.URI)
	if err != nil {
		s.respondNodeError(w, err)
		return
	}

	writeJSON(w, map[string]any{
		"name":          node.Name,
		"uri":           node.URI,
		"outbound_json": node.OutboundJSON,
	})
}

// handleConfigNodes handles GET (list) and POST (create) for config nodes.
func (s *Server) handleConfigNodes(w http.ResponseWriter, r *http.Request) {
	if !s.ensureNodeManager(w) {
		return
	}

	switch r.Method {
	case http.MethodGet:
		nodes, err := s.nodeMgr.ListConfigNodes(r.Context())
		if err != nil {
			s.respondNodeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"nodes": nodes})
	case http.MethodPost:
		var payload nodePayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			writeJSON(w, map[string]any{"error": "请求格式错误"})
			return
		}
		node, err := s.nodeMgr.CreateNode(r.Context(), payload.toConfig())
		if err != nil {
			s.respondNodeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"node": node, "message": "节点已添加，请点击重载使配置生效"})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleConfigNodeItem handles PUT (update) and DELETE for a specific config node.
func (s *Server) handleConfigNodeItem(w http.ResponseWriter, r *http.Request) {
	if !s.ensureNodeManager(w) {
		return
	}

	namePart := strings.TrimPrefix(r.URL.Path, "/api/nodes/config/")
	nodeName, err := url.PathUnescape(namePart)
	if err != nil || nodeName == "" {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]any{"error": "节点名称无效"})
		return
	}

	switch r.Method {
	case http.MethodPut:
		var payload nodePayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			writeJSON(w, map[string]any{"error": "请求格式错误"})
			return
		}
		node, err := s.nodeMgr.UpdateNode(r.Context(), nodeName, payload.toConfig())
		if err != nil {
			s.respondNodeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"node": node, "message": "节点已更新，请点击重载使配置生效"})
	case http.MethodPatch:
		var payload struct {
			Enabled *bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			writeJSON(w, map[string]any{"error": "请求格式错误"})
			return
		}
		if payload.Enabled == nil {
			w.WriteHeader(http.StatusBadRequest)
			writeJSON(w, map[string]any{"error": "缺少 enabled 字段"})
			return
		}
		if err := s.nodeMgr.SetNodeEnabled(r.Context(), nodeName, *payload.Enabled); err != nil {
			s.respondNodeError(w, err)
			return
		}
		action := "启用"
		if !*payload.Enabled {
			action = "禁用"
		}
		writeJSON(w, map[string]any{
			"message":     fmt.Sprintf("节点已%s，请点击重载使配置生效", action),
			"need_reload": true,
		})
	case http.MethodDelete:
		if err := s.nodeMgr.DeleteNode(r.Context(), nodeName); err != nil {
			s.respondNodeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"message": "节点已删除，请点击重载使配置生效"})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleConfigNodesBatchToggle handles batch enable/disable for config nodes.
func (s *Server) handleConfigNodesBatchToggle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !s.ensureNodeManager(w) {
		return
	}

	var payload struct {
		Names   []string `json:"names"`
		Enabled bool     `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]any{"error": "请求格式错误"})
		return
	}
	if len(payload.Names) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]any{"error": "节点列表为空"})
		return
	}

	var errs []string
	success := 0
	for _, name := range payload.Names {
		name = strings.TrimSpace(name)
		if name == "" {
			errs = append(errs, "空节点名称")
			continue
		}
		if err := s.nodeMgr.SetNodeEnabled(r.Context(), name, payload.Enabled); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		success++
	}

	action := "启用"
	if !payload.Enabled {
		action = "禁用"
	}
	result := map[string]any{
		"message":     fmt.Sprintf("成功%s %d 个节点，请点击重载使配置生效", action, success),
		"success":     success,
		"total":       len(payload.Names),
		"need_reload": success > 0,
	}
	if len(errs) > 0 {
		result["errors"] = errs
	}
	writeJSON(w, result)
}

// handleConfigNodesBatchDelete handles batch deletion for config nodes.
func (s *Server) handleConfigNodesBatchDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !s.ensureNodeManager(w) {
		return
	}

	var payload struct {
		Names []string `json:"names"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]any{"error": "请求格式错误"})
		return
	}
	if len(payload.Names) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]any{"error": "节点列表为空"})
		return
	}

	var errs []string
	success := 0
	skipped := 0
	sourcesByName := map[string]config.NodeSource{}
	if nodes, err := s.nodeMgr.ListConfigNodes(r.Context()); err == nil {
		for _, node := range nodes {
			sourcesByName[node.Name] = node.Source
		}
	}
	for _, name := range payload.Names {
		name = strings.TrimSpace(name)
		if name == "" {
			errs = append(errs, "空节点名称")
			continue
		}
		if sourcesByName[name] == config.NodeSourceSubscription {
			skipped++
			continue
		}
		if err := s.nodeMgr.DeleteNode(r.Context(), name); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		success++
	}

	message := fmt.Sprintf("成功删除 %d 个节点，请点击重载使配置生效", success)
	if skipped > 0 {
		message = fmt.Sprintf("成功删除 %d 个节点，跳过 %d 个订阅节点，请点击重载使配置生效", success, skipped)
	}
	result := map[string]any{
		"message":     message,
		"success":     success,
		"skipped":     skipped,
		"total":       len(payload.Names),
		"need_reload": success > 0,
	}
	if len(errs) > 0 {
		result["errors"] = errs
	}
	writeJSON(w, result)
}

// handleGeoIPRefresh forces a GeoIP database download.
func (s *Server) handleGeoIPRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	s.cfgMu.Lock()
	if s.cfgSrc == nil {
		s.cfgMu.Unlock()
		w.WriteHeader(http.StatusInternalServerError)
		writeJSON(w, map[string]any{"error": "配置存储未初始化"})
		return
	}
	dbPath := config.DefaultGeoIPDatabasePath()
	changed := strings.TrimSpace(s.cfgSrc.GeoIP.DatabasePath) != dbPath
	s.cfgSrc.GeoIP.DatabasePath = dbPath
	geoIPEnabled := s.cfgSrc.GeoIP.Enabled
	if changed && s.store != nil {
		if err := config.SaveRuntime(r.Context(), s.store, s.cfgSrc); err != nil {
			s.cfgMu.Unlock()
			w.WriteHeader(http.StatusInternalServerError)
			writeJSON(w, map[string]any{"error": fmt.Sprintf("保存 GeoIP 配置失败: %v", err)})
			return
		}
	}
	s.cfgMu.Unlock()

	if err := geoip.RefreshDatabase(dbPath); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		writeJSON(w, map[string]any{"error": fmt.Sprintf("刷新 GeoIP 数据库失败: %v", err)})
		return
	}

	writeJSON(w, map[string]any{
		"message":     "GeoIP 数据库已重新下载",
		"path":        dbPath,
		"need_reload": geoIPEnabled,
	})
}

// handleReload triggers a configuration reload.
func (s *Server) handleReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !s.ensureNodeManager(w) {
		return
	}

	if err := s.nodeMgr.TriggerReload(r.Context()); err != nil {
		s.respondNodeError(w, err)
		return
	}
	writeJSON(w, map[string]any{
		"message": "重载成功，现有连接已被中断",
	})
}

func (s *Server) ensureNodeManager(w http.ResponseWriter) bool {
	if s.nodeMgr == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		writeJSON(w, map[string]any{"error": "节点管理未启用"})
		return false
	}
	return true
}

func (s *Server) respondNodeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, ErrNodeNotFound):
		status = http.StatusNotFound
	case errors.Is(err, ErrNodeConflict), errors.Is(err, ErrInvalidNode), errors.Is(err, ErrNodeReadOnly):
		status = http.StatusBadRequest
	}
	w.WriteHeader(status)
	writeJSON(w, map[string]any{"error": err.Error()})
}

// handleTraffic streams real-time traffic from sing-box Clash API as SSE.
// Clash API /traffic returns newline-delimited JSON; we convert to SSE for browser EventSource.
func (s *Server) handleTraffic(w http.ResponseWriter, r *http.Request) {
	// Connect to sing-box Clash API
	resp, err := http.Get("http://127.0.0.1:9092/traffic")
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		writeJSON(w, map[string]any{"error": "无法连接到流量统计接口", "details": err.Error()})
		return
	}
	defer resp.Body.Close()

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Read NDJSON lines from Clash API and forward as SSE
	buf := make([]byte, 4096)
	for {
		select {
		case <-r.Context().Done():
			return
		default:
		}
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			// Each chunk may contain one or more JSON lines; forward as-is in SSE data frames
			lines := strings.Split(strings.TrimSpace(string(buf[:n])), "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				fmt.Fprintf(w, "data: %s\n\n", line)
			}
			flusher.Flush()
		}
		if readErr != nil {
			return
		}
	}
}

func (s *Server) handleTrafficStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	send := func(summary TrafficSummary) bool {
		data, err := json.Marshal(map[string]any{
			"type":           "traffic",
			"node_count":     summary.NodeCount,
			"total_upload":   summary.TotalUpload,
			"total_download": summary.TotalDownload,
			"upload_speed":   summary.UploadSpeed,
			"download_speed": summary.DownloadSpeed,
			"sampled_at":     summary.SampledAt,
			"nodes":          summary.Nodes,
		})
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	if !send(s.mgr.TrafficSummary(true)) {
		return
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if !send(s.mgr.TrafficSummary(true)) {
				return
			}
		}
	}
}

// handleLogs returns recent console log content from the in-memory ring buffer.
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	content := SharedLogBuffer.Content()
	writeJSON(w, map[string]any{"logs": content})
}

// Session management functions

// generateSessionToken creates a cryptographically secure random token.
func (s *Server) generateSessionToken() (string, error) {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", fmt.Errorf("failed to generate session token: %w", err)
	}
	return hex.EncodeToString(tokenBytes), nil
}

// createSession creates a new database-backed session with expiration.
func (s *Server) createSession() (*store.Session, error) {
	if s.store == nil {
		return nil, errors.New("database store is required for sessions")
	}
	token, err := s.generateSessionToken()
	if err != nil {
		return nil, err
	}

	now := time.Now()
	session := &store.Session{
		Token:     token,
		CreatedAt: now,
		ExpiresAt: now.Add(s.sessionTTL),
	}

	if err := s.store.CreateSession(context.Background(), session); err != nil {
		return nil, fmt.Errorf("persist session: %w", err)
	}

	return session, nil
}

// validateSession checks if a session token is valid and not expired.
func (s *Server) validateSession(token string) bool {
	if s.store == nil {
		return false
	}
	session, err := s.store.GetSession(context.Background(), token)
	if err != nil {
		s.logger.Printf("failed to load session: %v", err)
		return false
	}
	if session == nil {
		return false
	}
	if time.Now().After(session.ExpiresAt) {
		_ = s.store.DeleteSession(context.Background(), token)
		return false
	}
	return true
}

// cleanupExpiredSessions periodically removes expired sessions.
func (s *Server) cleanupExpiredSessions() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		if s.store == nil {
			continue
		}
		if err := s.store.CleanupExpiredSessions(context.Background()); err != nil {
			s.logger.Printf("failed to clean sessions: %v", err)
		}
	}
}

// secureCompareStrings performs constant-time string comparison to prevent timing attacks.
func secureCompareStrings(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
