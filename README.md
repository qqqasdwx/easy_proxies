# Easy Proxies

简体中文 | [English](README_EN.md)

Easy Proxies 是一个基于 [sing-box](https://sing-box.sagernet.org/) 的代理池管理工具，用于把多个上游代理节点统一管理为稳定、可观测、可动态调整的本地代理服务。它适合需要批量维护节点、自动健康检查、订阅同步、按节点独立端口访问，以及通过 WebUI 管理运行配置的场景。

## 上游致谢与项目独立说明

本项目最初基于 [jasonwong1991/easy_proxies](https://github.com/jasonwong1991/easy_proxies) 开发，感谢原作者提供的基础实现和开源工作。

自 **2026-05-11** 起，本仓库已作为独立项目维护。由于 v2.0.0 引入了破坏性更新，包括废弃 `config.yaml` / `nodes.txt`、改为 SQLite 作为唯一持久化来源、重构 WebUI 和运行配置模型，本项目已无法与上游仓库保持配置和行为兼容。后续如需引入上游修复，将以人工评估和移植的方式处理，不再直接同步上游分支。

## 核心特性

- **三种运行模式**：`pool` 单入口代理池、`multi-port` 每节点独立端口、`hybrid` 混合模式。
- **多协议节点支持**：VLESS、VMess、Trojan、Shadowsocks、Hysteria2、TUIC、AnyTLS、SOCKS5、HTTP/HTTPS。
- **SQLite 持久化**：运行设置、手工节点、订阅源、订阅节点、端口分配、禁用状态、会话和流量统计均存储在数据库中。
- **订阅管理**：支持多个订阅源，支持启用/禁用、自动更新、刷新间隔、手动刷新和刷新状态展示。
- **节点管理**：手工节点和订阅节点共存；手工节点可编辑，订阅节点只读但可启停。
- **结构化节点编辑器**：支持 URI 导入、sing-box outbound JSON 编辑、表单同步和每节点本地入站设置。
- **健康检查与熔断**：自动探测节点可用性，支持失败黑名单、手动拉黑和手动解封。
- **GeoIP 分区路由**：按节点地域分组，并提供独立 HTTP 代理入口按区域出站。
- **WebUI 与管理 API**：提供节点监控、订阅管理、日志控制台、运行设置和诊断接口。
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
| `MANAGEMENT_PASSWORD` | 启用 WebUI/API 登录密码；不会写入数据库、API 响应、前端表单或日志 |

更多说明见 [SQLite Runtime Store](docs/sqlite-migration.md)。

## 运行模式

| 模式 | 说明 |
| --- | --- |
| `pool` | 所有可用节点共享一个本地 HTTP/SOCKS5 入口，并按调度策略选择出站节点 |
| `multi-port` | 每个节点分配独立本地端口，适合需要固定访问某个节点的场景 |
| `hybrid` | 同时启用代理池入口和每节点独立端口 |

入口协议由 WebUI「系统设置」维护，支持 `mixed`、`http` 和 `socks5`。

## 节点与订阅

手工节点可通过 WebUI 或 `POST /api/nodes/config` 添加。节点编辑器支持直接粘贴 URI、编辑 sing-box outbound JSON，并通过表单修改常用字段。

订阅在 WebUI「订阅管理」中维护，也可通过 `/api/subscriptions` API 操作。每个订阅可以独立设置启用状态、自动更新和刷新间隔。订阅节点会展示在节点管理中，可以启停和查看，但不支持直接编辑。

## GeoIP 分区路由

启用 GeoIP 后，系统会下载并维护 GeoIP 数据库，将节点按 `jp`、`kr`、`us`、`hk`、`tw`、`sg`、`other` 分组。GeoIP 路由器提供独立 HTTP 代理入口，可通过路径选择区域：

```bash
curl -x http://user:pass@localhost:1221/jp/ http://example.com
curl -x http://user:pass@localhost:1221/us/ http://example.com
```

数据库文件路径、最近更新时间和手动刷新按钮可在 WebUI 中查看。数据库实际存放位置由程序管理：Docker 中为 `/app/data`，本地运行为 `data/`。

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
- `GET|POST /api/nodes/config`
- `PUT|DELETE|PATCH /api/nodes/config/{name}`
- `POST /api/geoip/refresh`
- `GET /api/logs`
- `POST /api/reload`

## 开发与验证

```bash
go test ./...
npm ci --prefix frontend
npm --prefix frontend run lint
npm --prefix frontend run build
docker build -t easy_proxies:dev .
```

运行 Docker 端到端回归：

```bash
scripts/e2e/docker-proxy-flow.sh
```

该脚本会构建临时镜像，启动本地 HTTP 目标、SOCKS 上游和 Easy Proxies 容器，并验证 `pool`、`multi-port`、`hybrid`、订阅刷新和重启持久化。

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
