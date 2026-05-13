package builder

import (
	"encoding/base64"
	"net/url"
	"strings"
	"testing"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
)

func TestBuildNodeOutbound_Hysteria2PortRangeInRawURI(t *testing.T) {
	outbound, err := buildNodeOutbound("test-hy2", "hysteria2://secret@example.com:10000-20000?sni=hy2.example.com", false)
	if err != nil {
		t.Fatalf("build node outbound failed: %v", err)
	}

	opts, ok := outbound.Options.(*option.Hysteria2OutboundOptions)
	if !ok {
		t.Fatalf("expected *option.Hysteria2OutboundOptions, got %T", outbound.Options)
	}

	if opts.Server != "example.com" {
		t.Fatalf("expected server example.com, got %q", opts.Server)
	}
	if opts.ServerPort != 443 {
		t.Fatalf("expected default server port 443, got %d", opts.ServerPort)
	}
	if len(opts.ServerPorts) != 1 || opts.ServerPorts[0] != "10000:20000" {
		t.Fatalf("expected server ports [10000:20000], got %v", opts.ServerPorts)
	}
}

func TestBuildHysteria2Options_PortsFromQuery(t *testing.T) {
	u, err := url.Parse("hysteria2://secret@example.com:443?ports=10000-20000,30000")
	if err != nil {
		t.Fatalf("parse uri failed: %v", err)
	}

	opts, err := buildHysteria2Options(u, false)
	if err != nil {
		t.Fatalf("build hysteria2 options failed: %v", err)
	}

	if len(opts.ServerPorts) != 2 {
		t.Fatalf("expected 2 server ports, got %d (%v)", len(opts.ServerPorts), opts.ServerPorts)
	}
	if opts.ServerPorts[0] != "10000:20000" || opts.ServerPorts[1] != "30000" {
		t.Fatalf("unexpected server ports: %v", opts.ServerPorts)
	}
}

func TestBuildV2RayTransport_QUICSupported(t *testing.T) {
	outbound, err := buildNodeOutbound("vless-quic", "vless://bf000d23-0752-40b4-affe-68f7707a9661@example.com:443?type=quic&security=tls", false)
	if err != nil {
		t.Fatalf("build node outbound failed: %v", err)
	}

	opts, ok := outbound.Options.(*option.VLESSOutboundOptions)
	if !ok {
		t.Fatalf("expected *option.VLESSOutboundOptions, got %T", outbound.Options)
	}
	if opts.Transport == nil || opts.Transport.Type != C.V2RayTransportTypeQUIC {
		t.Fatalf("expected quic transport, got %#v", opts.Transport)
	}
}

func TestBuildV2RayTransport_HTTPUpgradeHost(t *testing.T) {
	outbound, err := buildNodeOutbound("vless-httpupgrade", "vless://bf000d23-0752-40b4-affe-68f7707a9661@example.com:443?type=httpupgrade&path=%2Fup&host=edge.example.com", false)
	if err != nil {
		t.Fatalf("build node outbound failed: %v", err)
	}

	opts, ok := outbound.Options.(*option.VLESSOutboundOptions)
	if !ok {
		t.Fatalf("expected *option.VLESSOutboundOptions, got %T", outbound.Options)
	}
	if opts.Transport == nil || opts.Transport.HTTPUpgradeOptions.Host != "edge.example.com" {
		t.Fatalf("expected httpupgrade host edge.example.com, got %#v", opts.Transport)
	}
}

func TestBuildShadowsocksOptions_PluginFromURI(t *testing.T) {
	userInfo := base64.RawURLEncoding.EncodeToString([]byte("aes-128-gcm:secret"))
	outbound, err := buildNodeOutbound("ss-plugin", "ss://"+userInfo+"@example.com:8388?plugin=obfs-local&plugin_opts=obfs%3Dhttp%3Bobfs-host%3Dedge.example.com", false)
	if err != nil {
		t.Fatalf("build node outbound failed: %v", err)
	}

	opts, ok := outbound.Options.(*option.ShadowsocksOutboundOptions)
	if !ok {
		t.Fatalf("expected *option.ShadowsocksOutboundOptions, got %T", outbound.Options)
	}
	if opts.Plugin != "obfs-local" || opts.PluginOptions != "obfs=http;obfs-host=edge.example.com" {
		t.Fatalf("unexpected plugin options: plugin=%q opts=%q", opts.Plugin, opts.PluginOptions)
	}
}

func TestNormalizeOutboundJSON_EnablesRequiredTLS(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{
			name: "hysteria2",
			raw:  `{"type":"hysteria2","server":"example.com","server_port":443,"password":"secret","tls":{}}`,
		},
		{
			name: "tuic",
			raw:  `{"type":"tuic","server":"example.com","server_port":443,"uuid":"bf000d23-0752-40b4-affe-68f7707a9661","password":"secret","tls":{}}`,
		},
		{
			name: "anytls",
			raw:  `{"type":"anytls","server":"example.com","server_port":443,"password":"secret","tls":{}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			normalized, err := NormalizeOutboundJSON(tt.name, tt.raw)
			if err != nil {
				t.Fatalf("normalize outbound json: %v", err)
			}
			if !strings.Contains(normalized, `"enabled": true`) {
				t.Fatalf("expected tls.enabled true in normalized JSON:\n%s", normalized)
			}
		})
	}
}
