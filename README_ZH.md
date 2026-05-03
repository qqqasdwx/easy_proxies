# Easy Proxies

[English](README.md) | 简体中文

Easy Proxies 是一个基于 sing-box 的代理池管理工具。

目标是把大量上游节点统一成稳定的本地 HTTP/SOCKS5 代理入口，同时支持按节点独立端口访问。

## 当前能力

- 运行模式：`pool`、`multi-port`、`hybrid`。
- 实际构建的上游协议：`vmess`、`vless`、`trojan`、`ss/shadowsocks`、`hysteria2/hy2`、`socks5/socks`、`http/https`、`anytls`、`tuic`。
- 节点来源：
  - WebUI/API 手工节点
  - 多个订阅源（支持 Base64/纯文本/Clash YAML 解析）
  - 所有节点都持久化在 SQLite 中
- 自动健康检查、失败熔断和黑名单恢复。
- Web 管理面板 + API：
  - 节点状态/探测/导出
  - **手动拉黑/解封节点**
  - 动态设置（`external_ip`、`probe_target`、`skip_cert_verify`、`geoip`）
  - 节点配置增删改查 + 重载
  - 订阅状态查询 + 手动刷新 + **保存即时生效**
  - **日志控制台**（最近 1000 行，WebUI 自动刷新）
- 新增可配置 DNS 解析器（对 VMess 域名节点非常关键）。
- 可选 GeoIP 标记（支持 JP/KR/US/HK/TW/SG 地域分区，可在 WebUI 中开关，支持自动更新和热重载）。
- **可配置日志轮转**，支持大小限制、备份数量和压缩。
- **SQLite store** 是唯一持久化来源，保存运行设置、节点、订阅源、禁用状态、会话和流量统计。

## 快速开始

### 1）启动管理端

```bash
mkdir -p logs data
docker compose up -d
```

默认不需要 `config.yaml` 或 `nodes.txt`，首次启动只监听管理端 `9091`。

管理端口和登录密码通过 Docker 环境变量设置：

```bash
MANAGEMENT_PORT=19091 MANAGEMENT_PASSWORD='change-me' docker compose up -d
```

本地运行：

```bash
go run ./cmd/easy_proxies --database data/data.db
```

## 运行状态与持久化

Easy Proxies 不再读取或写入 `config.yaml`、`nodes.txt`。启动流程是：代码默认值 -> SQLite 中的运行设置/节点/订阅/会话 -> 环境变量覆盖 -> 构建 sing-box 配置。

本地开发默认使用 `data/data.db`，也可以通过 `--database` 指定；Docker 内固定挂载 `./data:/app/data` 来持久化 `/app/data/data.db`。

`MANAGEMENT_PORT` 只在进程启动时覆盖管理端口。`MANAGEMENT_PASSWORD` 只从环境变量读取，不会进入数据库、API 响应、前端表单或日志。

## DNS 配置说明

`dns` 会同时影响 sing-box DNS 客户端和 VMess 域名拨号解析：

```yaml
dns:
  server: 223.5.5.5
  fallback_servers:    # 备用 DNS 服务器（主 DNS 解析失败时使用）
    - 8.8.8.8
    - 1.1.1.1
  port: 53
  strategy: prefer_ipv4
```

`strategy` 可选值：

- `as_is`
- `prefer_ipv4`
- `prefer_ipv6`
- `ipv4_only`
- `ipv6_only`

如果日志中出现 `lookup <domain>: empty result`，请优先检查该 DNS 配置是否可达且策略合理。

## 运行模式

- `pool`：所有节点共享一个本地 HTTP/SOCKS5 入口。
- `multi-port`：每个节点一个独立本地 HTTP/SOCKS5 端口。
- `hybrid`：同时启用 pool + multi-port。

## 入站协议

`listener.protocol` 控制 pool 入口协议，`multi_port.protocol` 控制多端口入口协议。可选值为 `mixed`（默认，HTTP + SOCKS5）、`http`、`socks5`。

## 节点来源行为

手工节点通过 WebUI 或 `POST /api/nodes/config` 添加，支持 URI 导入、结构化 sing-box outbound JSON 和每个节点的本地入站设置。

订阅在独立菜单或 `/api/subscriptions` 中管理。多个订阅可以共存，每个订阅都有启用、自动更新、刷新间隔和刷新状态。订阅节点会出现在节点管理中，可以启停和查看，但不允许编辑或删除。

## SQLite 运行数据

新部署只需要创建 `data/` 和 `logs/`：

```bash
mkdir -p data logs
docker compose up -d
```

运行设置、节点、订阅源、登录会话、端口分配、禁用状态和流量累计会写入 `data/data.db`。升级和备份时至少保留：

- `data/`
- `logs/`

更详细的说明见 [SQLite Runtime Store](docs/sqlite-migration.md)。

## 协议支持注意事项

运行时真正支持的协议：

- `vmess`
- `vless`
- `trojan`
- `ss` / `shadowsocks`
- `hysteria2` / `hy2`
- `socks5` / `socks`
- `http` / `https`
- `anytls`
- `tuic`

订阅解析阶段可能识别到更多 URI 前缀（兼容输入），但不在上述列表中的协议会在构建阶段被跳过。

## 管理 API（核心）

- `POST /api/auth`
- `GET|PUT /api/settings`
- `GET /api/nodes`
- `POST /api/nodes/{tag}/probe`
- `POST /api/nodes/{tag}/release`
- `POST /api/nodes/{tag}/blacklist`
- `POST /api/nodes/probe-all`（SSE）
- `GET /api/nodes/traffic/stream`（SSE）
- `GET /api/export`
- `POST /api/import`
- `GET|POST /api/subscriptions`
- `PUT|DELETE /api/subscriptions/{id}`
- `POST /api/subscriptions/{id}/refresh`
- `GET /api/subscription/status`
- `POST /api/geoip/refresh`
- `GET|POST /api/nodes/config`
- `PUT|DELETE|PATCH /api/nodes/config/{name}`
- `POST /api/nodes/config/batch-toggle`
- `POST /api/nodes/config/batch-delete`
- `GET /api/logs`
- `POST /api/reload`

设置 `MANAGEMENT_PASSWORD` 后 Web/API 才要求登录；管理密码不作为配置项出现。

## 重要运行说明

- 重载（`/api/reload` 或订阅刷新）会中断现有连接。
- Settings API 修改运行时设置；节点、订阅、会话和统计数据持久化到 SQLite。
- 省略项默认值可在 `internal/config/config.go` 中查看。
- 日志轮转通过 `log` 配置段设置；当 `output: file` 时，日志同时写入控制台和文件，并自动轮转。

## 更新日志

详见 [CHANGELOG.md](CHANGELOG.md)。

## 开发验证

```bash
go test ./...
npm ci --prefix frontend
npm run build --prefix frontend
docker build -t easy_proxies:dev .
```

## Star History

[![Star History Chart](https://api.star-history.com/svg?repos=jasonwong1991/easy_proxies&type=Date)](https://star-history.com/#jasonwong1991/easy_proxies&Date)

## 许可证

MIT License
