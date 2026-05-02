# 数据库唯一持久化与订阅节点编辑开发计划

## 决策

本轮改造不再兼容旧用户的 `config.yaml` 和 `nodes.txt`。SQLite 是唯一持久化来源；代码内默认值只用于首次启动和缺省字段补齐。启动时不读取、不写入、不迁移旧 YAML 或节点文件，相关文档、脚本、API 和 UI 文案都要同步删除旧概念。

环境变量只作为运行时覆盖项：`MANAGEMENT_PORT` 覆盖管理端口，`MANAGEMENT_PASSWORD` 只从容器环境变量读取，永远不进入数据库、API 响应、前端表单或日志。容器内数据库固定使用 `/app/data/data.db`，本地开发默认 `data/data.db`。

## 当前设计评估

- `cmd/easy_proxies/main.go` 仍默认 `--config config.yaml`，并通过 `config.Load()` 构造运行配置。
- `internal/config.Config` 同时承载 YAML、默认值、运行态和保存逻辑；`SaveSettings()`、`SaveNodes()` 都是文件写回模型，文件不存在时直接 no-op，导致 WebUI 修改重启后丢失。
- `normalize()` 会读取 `nodes_file`，也会启动时拉取 `subscriptions`，并在部分场景写回 `nodes.txt`。
- SQLite store 已有 `nodes`、`node_stats`、`node_timeline`、`sessions`、`subscription_status`，但应用仍先从配置文件同步到 store，而不是从 store 生成运行配置。
- WebUI 会话仍存在 `monitor.Server.sessions` 内存 map，未使用现有 `sessions` 表。
- 订阅来源仍是 `cfg.Subscriptions []string`，订阅刷新状态主要在内存里，`subscription_status` 表没有成为真实状态源。
- `boxmgr.Manager.CreateNode()` 在存在订阅时会把 WebUI 新增节点标记为 `subscription`，手工节点与订阅节点来源语义不可靠。

## 必须持久化的内容

- 运行设置：`mode`、连接池策略、失败阈值、黑名单时间、监听地址/端口/协议、multi-port 基础端口、代理用户名密码、外部 IP、跳过证书校验、日志设置、GeoIP 设置、订阅刷新默认超时和排空策略。
- 管理端设置：是否启用、监听地址、探测目标需要入库；管理密码不入库，端口可被 `MANAGEMENT_PORT` 覆盖。
- 节点：手工添加、批量导入、订阅拉取的节点都写入 `nodes` 表。订阅节点需要 `subscription_id`，并且在节点管理中只读。
- 订阅源：多个订阅的 `name/url/enabled/auto_update/interval/timeout/last_refresh/next_refresh/node_count/last_error`。
- 运行状态：节点启停、端口分配、延迟、成功/失败次数、黑名单、流量统计、时间线。
- 登录会话：使用现有 `sessions` 表，重启后未过期会话仍有效。
- 订阅刷新状态：按订阅源记录刷新中、刷新次数、节点 hash、错误信息和下次刷新时间。

日志文件和 GeoIP `.mmdb` 是运行产物或外部资源，不作为应用配置放入数据库。

## 目标数据模型

- `app_settings`：单行或 key/value JSON，保存运行设置；由 store 提供强类型读写接口。
- `subscription_sources`：订阅源表，包含开关、自动更新、刷新间隔、状态字段。
- `nodes`：保留基础字段，新增 `subscription_id`、`outbound_json`、`inbound_protocol`；来源只允许 `manual`、`subscription`。
- `sessions`：复用现有表，移除 monitor 内存会话作为真实来源。
- `subscription_status`：改为按订阅源记录，或合并进 `subscription_sources` 状态字段。

## 分支策略

所有阶段从 `dev` 签出独立分支，完成后合回 `dev`。

```bash
git switch dev
git switch -c feat/db-only-persistence
```

## 阶段 1：数据库唯一持久化基座

建议分支：`feat/db-only-persistence`

任务：

- 移除启动对 `--config`、`config.yaml`、`nodes.txt`、`nodes_file`、YAML `subscriptions` 的依赖。
- 新增 store API：`LoadRuntimeConfig()`、`SaveRuntimeConfig()`、订阅源 CRUD、订阅状态更新、会话 CRUD 接入。
- 启动流程改为：代码默认值 -> SQLite 设置 -> SQLite 节点/订阅 -> 环境变量覆盖 -> 构建 sing-box 配置。
- WebUI `PUT /api/settings`、订阅配置、节点增删改全部写 SQLite；取消 `SaveSettings()`/`SaveNodes()` 文件写回路径。
- 订阅刷新只 upsert `source=subscription` 且匹配 `subscription_id` 的节点，不覆盖手工节点。
- 使用 `sessions` 表替代内存会话 map。

验收：

- 删除 `config.yaml` 和 `nodes.txt` 后可正常启动，且不会重新创建它们。
- 修改模式、监听端口、代理账号、GeoIP、订阅、节点后重启仍保留。
- `MANAGEMENT_PASSWORD` 不出现在数据库、API 响应、前端表单和日志中。
- `go test ./...` 通过，并补充 settings store、会话、订阅源、节点来源测试。

## 阶段 2：订阅管理独立菜单

建议分支：`feat/subscriptions-page`

任务：

- 新增 `GET/POST/PUT/DELETE /api/subscriptions` 和单订阅刷新接口。
- 前端新增“订阅管理”菜单，支持多个订阅源、启停、自动更新开关、间隔、手动刷新和状态展示。
- 从系统设置页移除订阅链接输入，仅保留全局刷新超时、健康检查和排空策略。

验收：多个订阅可共存；关闭自动更新不影响手动刷新；刷新状态重启后仍可见。

## 阶段 3：手工节点与订阅节点共存

建议分支：`fix/node-source-semantics`

任务：

- WebUI 创建、导入的节点一律为 `manual`。
- 订阅节点在节点管理中显示但禁止编辑和删除，只允许启停、探测和查看。
- 批量删除跳过订阅节点并返回明确提示。

验收：有订阅时新增手工节点，刷新订阅和重启后仍存在并参与三种代理模式。

## 阶段 4：结构化节点编辑

建议分支：`feat/structured-node-editor`

任务：

- 增加 sing-box outbound JSON 存储，URI 作为导入/导出格式而非唯一编辑格式。
- 节点编辑弹窗改为 `Form`、`JSON`、`入站` 三个 tab。
- Form 首期覆盖当前 builder 已支持的 `socks/http/shadowsocks/vmess/vless/trojan/hysteria2/tuic/anytls`。
- “入站”tab 管理本地端口、监听协议、用户名和密码。

验收：Form 与 JSON 可互转；JSON 校验失败不落库；订阅节点打开为只读详情。

## 阶段 5：回归验证

建议分支：`test/db-only-e2e`

任务：

- Go 单测覆盖设置持久化、订阅源、会话、节点来源保护和 reload。
- Docker host 网络启动项目，另起 SOCKS 代理容器作为真实节点。
- Playwright 跑通 pool、multi-port、hybrid 三种模式，以及订阅管理、手工节点、只读订阅节点、重启持久化。

验收：`go test ./...`、前端 build、Docker + Playwright E2E 全部通过。
