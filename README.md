# Easy Proxies

简体中文 | [English](README_EN.md)

Easy Proxies 是一个基于 [sing-box](https://sing-box.sagernet.org/) 的代理池管理工具，用于把多个上游代理节点统一管理为稳定、可观测、可动态调整的本地代理服务。它适合需要批量维护节点、自动健康检查、订阅同步、按节点独立端口访问，以及通过 WebUI 管理运行配置的场景。

## 上游致谢与项目独立说明

本项目最初基于 [jasonwong1991/easy_proxies](https://github.com/jasonwong1991/easy_proxies) 开发，感谢原作者提供的基础实现和开源工作。

自 **2026-05-11** 起，本仓库已作为独立项目维护。由于 v2.0.0 引入了破坏性更新，包括废弃 `config.yaml` / `nodes.txt`、改为 SQLite 作为唯一持久化来源、重构 WebUI 和运行配置模型，本项目已无法与上游仓库保持配置和行为兼容。后续如需引入上游修复，将以人工评估和移植的方式处理，不再直接同步上游分支。

## 核心特性

- **多代理池入口**：可创建多个代理池，每个代理池独立配置监听、调度策略和成员节点。
- **多协议节点支持**：VLESS、VMess、Trojan、Shadowsocks、Hysteria2、TUIC、AnyTLS、SOCKS5、HTTP/HTTPS。
- **SQLite 持久化**：运行设置、手工节点、订阅源、订阅节点、端口分配、禁用状态、会话和流量统计均存储在数据库中。
- **订阅管理**：支持多个订阅源，支持启用/禁用、自动更新、刷新间隔、手动刷新和刷新状态展示。
- **节点管理**：手工节点和订阅节点共存；手工节点可编辑，订阅节点只读但可启停。
- **结构化节点编辑器**：支持 URI 导入、sing-box outbound JSON 编辑、表单同步和每节点本地入站设置。
- **健康检查与熔断**：自动探测节点可用性，支持失败黑名单、手动拉黑和手动解封。
- **GeoIP 分区路由**：全局启用后，所有代理池都会按节点地域生成 HTTP 路由入口。
- **WebUI 与管理 API**：提供节点监控、代理池管理、订阅管理、日志控制台、系统设置和诊断接口。
- **Docker 优先部署**：默认仅暴露管理端口，运行数据挂载在 `./data` 和 `./logs`。

## 快速开始

### Docker Compose

```bash
mkdir -p data logs
docker compose up -d
```

默认管理地址为：

```text
http://localhost:9091
```

管理端口和登录密码只通过环境变量设置：

```bash
MANAGEMENT_PORT=19091 MANAGEMENT_PASSWORD='change-me' docker compose up -d
```

默认 `docker-compose.yml` 只把 WebUI/API 绑定到宿主机 `127.0.0.1`，避免空密码部署时暴露到外部网络。需要远程访问时，请先设置 `MANAGEMENT_PASSWORD`，再按部署环境显式调整端口发布或反向代理。

### 从源码运行

```bash
go run ./cmd/easy_proxies --database data/data.db
```

## 配置与持久化

Easy Proxies v2.0.0 起不再读取、写入或迁移 `config.yaml` 和 `nodes.txt`。启动流程为：

```text
内置默认值 -> SQLite 运行数据 -> 环境变量覆盖 -> 构建 sing-box 配置
```

Docker 部署时请持久化以下目录：

```yaml
volumes:
  - ./data:/app/data
  - ./logs:/app/logs
```

关键环境变量：

| 变量 | 说明 |
| --- | --- |
| `MANAGEMENT_PORT` | 进程启动时覆盖 WebUI/API 管理端口 |
| `MANAGEMENT_PASSWORD` | 启用 WebUI/API 登录密码；不会写入数据库、API 响应、前端表单或日志。对外暴露管理端时必须设置 |

更多说明见 [SQLite Runtime Store](docs/sqlite-migration.md)。

## 代理入口

Easy Proxies 不再提供全局运行模式切换。代理池入口和节点独立端口可以同时存在：

- WebUI「代理池管理」中可添加多个代理池；每个代理池有独立监听地址、端口、协议、调度策略和节点范围。
- WebUI「节点管理」中可给节点设置本地端口；端口不为 `0` 时，该节点会额外暴露独立入口。
- 入口协议支持 `mixed`、`http` 和 `socks5`。

## 节点与订阅

手工节点可通过 WebUI 或 `POST /api/nodes/config` 添加。节点编辑器支持直接粘贴 URI、编辑 sing-box outbound JSON，并通过表单修改常用字段。

订阅在 WebUI「订阅管理」中维护，也可通过 `/api/subscriptions` API 操作。每个订阅可以独立设置启用状态、自动更新和刷新间隔。订阅节点会展示在节点管理中，可以启停和查看，但不支持直接编辑。

## GeoIP 分区路由

启用 GeoIP 后，系统会下载并维护 GeoIP 数据库，将节点按 `jp`、`kr`、`us`、`hk`、`tw`、`sg`、`other` 分组。GeoIP 路由器提供独立 HTTP 代理入口，可通过路径选择区域：

```bash
curl -x http://user:pass@localhost:1221/1/jp/ http://example.com
curl -x http://user:pass@localhost:1221/1/us/ http://example.com
```

路径格式为 `/{proxy_pool_id}/{region}/`，例如 `/1/jp/`。数据库文件路径、最近更新时间和手动刷新按钮可在 WebUI「代理池管理」中查看。

## 管理 API

常用接口：

- `POST /api/auth`
- `GET|PUT /api/settings`
- `GET /api/nodes`
- `POST /api/nodes/{tag}/probe`
- `POST /api/nodes/{tag}/blacklist`
- `POST /api/nodes/{tag}/release`
- `POST /api/nodes/probe-all`
- `GET|POST /api/subscriptions`
- `PUT|DELETE /api/subscriptions/{id}`
- `POST /api/subscriptions/{id}/refresh`
- `GET|POST /api/proxy-pools`
- `PUT|DELETE /api/proxy-pools/{id}`
- `GET|POST /api/nodes/config`
- `PUT|DELETE|PATCH /api/nodes/config/{name}`
- `POST /api/geoip/refresh`
- `GET /api/logs`
- `POST /api/reload`

## 开发与验证

```bash
scripts/test-go.sh
npm ci --prefix frontend
npm --prefix frontend run lint
npm --prefix frontend run build
docker build -t easy_proxies:dev .
```

运行 Docker 端到端回归：

```bash
scripts/e2e/docker-proxy-flow.sh
```

该脚本会构建临时镜像，启动本地 HTTP 目标、SOCKS 上游和 Easy Proxies 容器，并验证代理入口、订阅刷新和重启持久化。

## 升级注意事项

v2.0.0 是破坏性版本：

- 不兼容旧版 `config.yaml` 和 `nodes.txt` 工作流。
- 不会自动迁移旧配置文件。
- 新部署请保留 `data/` 和 `logs/` 挂载目录。
- 管理密码必须通过 `MANAGEMENT_PASSWORD` 设置。

## Star History

[![Star History Chart](https://api.star-history.com/svg?repos=qqqasdwx/easy_proxies&type=Date)](https://star-history.com/#qqqasdwx/easy_proxies&Date)

## 许可证

MIT License
