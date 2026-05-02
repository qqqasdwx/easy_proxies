# EasyProxiesV2 功能合并开发计划

## 背景与目标

`references/EasyProxiesV2` 是基于早期 `easy_proxies` 二次开发的独立仓库，不保留上游 fork 历史。当前仓库是原项目最新版分支，已经包含 V2 缺失的后续修复与能力，例如 TUIC、Hysteria2 port hopping、GeoIP 独立路由、并发健康检查、手动 blacklist、日志轮转和更稳健的出站校验。

合并目标是以当前仓库为主线，选择性吸收 V2 增量功能，避免用 V2 旧代码覆盖当前主线能力。

## 合并原则

- 不整体替换 `internal/builder`、`internal/outbound/pool`、`internal/geoip`、`internal/boxmgr`。
- 每个功能独立拆分 PR/提交，保持可回滚。
- 以 `dev` 作为 V2 功能合并集成分支；每个阶段都从 `dev` 签出独立功能分支，验收通过后再合回 `dev`。
- 先补后端接口和数据模型，再接前端。
- 配置变更必须兼容现有 `config.yaml`、`nodes.txt`、Docker 部署方式。
- 涉及节点持久化、订阅、健康检查、热重载的改动必须补测试。

## 分支策略

### 基线分支

- `main`：跟随原项目最新版，保持可发布基线。
- `dev`：V2 功能合并集成分支，所有阶段功能先合入这里。
- `references/`：仅保存参考仓库，不提交、不作为代码来源分支。

### 阶段分支规则

每个阶段开始前从最新 `dev` 签出新分支：

```bash
git switch dev
git pull --ff-only
git switch -c feat/v2-<stage-name>
```

阶段完成并通过验证后，合回 `dev`：

```bash
git switch dev
git merge --no-ff feat/v2-<stage-name>
```

如果没有远端 `dev` 或当前环境不需要拉取，可跳过 `git pull --ff-only`。不在 `main` 上直接开发 V2 合并功能。

### 建议分支命名

- `feat/v2-baseline-audit`
- `feat/v2-inbound-protocol`
- `feat/v2-sqlite-store`
- `feat/v2-node-persistence`
- `feat/v2-batch-node-api`
- `feat/v2-traffic-stats`
- `feat/v2-react-webui`
- `docs/v2-migration-guide`
- `chore/v2-release-readiness`

## 阶段 0：基线确认

建议分支：`feat/v2-baseline-audit`

完成标记: done

### 任务

- 运行当前主线测试：`go test ./...`。
- 记录当前 API 路由、配置字段、Docker 行为。
- 建立 V2 功能清单与当前主线差异表。

### 验收

- 当前主线测试通过。
- 明确不迁移的 V2 旧实现列表。

## 阶段 1：入口协议配置

建议分支：`feat/v2-inbound-protocol`

完成标记: done

### 目标

合并 V2 的 `listener.protocol` 和 `multi_port.protocol`，支持 `http`、`socks5`、`mixed`。

### 实现

- 在 `internal/config/config.go` 增加协议字段、默认值和校验。
- 从 V2 移植并适配 `NormalizeInboundProtocol`。
- 在 `internal/builder/builder.go` 增加 `buildInboundByProtocol`。
- 保持旧配置无 `protocol` 时行为不变，默认 `mixed` 或当前等价行为。
- 更新 `config.example.yaml`、README 和导出逻辑。

### 测试

- 增加配置默认值测试。
- 增加 builder inbound 类型测试。
- 确认 `pool`、`multi-port`、`hybrid` 三种模式都可构建。

## 阶段 2：SQLite Store 基础层

建议分支：`feat/v2-sqlite-store`

完成标记: done

### 目标

引入可选 SQLite store，用于节点、统计、会话和订阅状态持久化，但不立即替换现有文件模型。

### 实现

- 新增 `internal/store/`，参考 V2 的接口、SQLite 实现和 migrations。
- 在 `go.mod` 加入 `modernc.org/sqlite`。
- 新增配置项 `database_path`，默认 `data/data.db`。
- Docker/Compose 增加 `./data` 挂载。
- `.gitignore` 增加 `data/` 和 `*.db`。

### 兼容策略

- 第一阶段 store 只作为可选组件。
- `config.yaml` 和 `nodes.txt` 仍是默认来源。
- 不改变现有 WebUI 编辑节点后的保存行为。

### 测试

- store migration 测试。
- CRUD、bulk upsert、session、subscription status 测试。

## 阶段 3：节点持久化双轨适配

建议分支：`feat/v2-node-persistence`

完成标记: done

### 目标

把 V2 的手动节点、订阅节点持久化能力接入当前主线，同时保留 `nodes.txt` 兼容。

### 实现

- 在 `boxmgr.Manager` 增加可选 `WithStore`。
- 节点新增/编辑/删除时，同时更新当前配置和 store。
- 增加节点 `enabled/disabled` 状态，不删除即可停用。
- 订阅刷新时优先写 store，并保留可配置的 `nodes.txt` 写回模式。
- 设计迁移逻辑：首次启动时从 `config.yaml` / `nodes.txt` seed 到 store。

### 风险

- 当前 `SaveNodes` 与 V2 store 模型冲突，不能直接删除。
- 订阅刷新、手动节点、inline nodes 的优先级需要明确。

### 验收

- 老用户不配置 `database_path` 仍可按原方式运行。
- 开启 store 后，WebUI 添加节点重启后仍存在。
- 禁用节点不参与 sing-box 构建和代理池调度。

## 阶段 4：批量节点管理与导入 API

建议分支：`feat/v2-batch-node-api`

完成标记: done

### 目标

合并 V2 的批量启停、批量删除、导入能力。

### API

- `POST /api/nodes/config/batch-toggle`
- `POST /api/nodes/config/batch-delete`
- `POST /api/import`

### 实现

- 扩展当前 `NodeManager` 接口。
- API 使用当前错误类型和鉴权中间件。
- 批量操作完成后触发 reload 或返回“需 reload”的明确状态。

### 测试

- 批量启停不会影响 inline/system 节点来源。
- 删除不存在节点时返回可理解的部分失败结果。

## 阶段 5：运行统计与流量持久化

建议分支：`feat/v2-traffic-stats`

完成标记: done

### 目标

参考 V2 引入节点级流量统计、速度采样、周期落库。

### 实现

- 在 `monitor.Manager` 增加 `TrafficSummary`、`AddTraffic`、`SetTraffic`。
- 在 pool shared state 中记录节点 upload/download。
- 新增 `GET /api/nodes/traffic/stream`，保留当前 `/api/traffic` 兼容。
- 周期 flush runtime stats 到 store。

### 风险

- 当前 `/api/traffic` 来自 sing-box Clash API，V2 是 manager 聚合模型。
- 需要确认两套流量口径是否一致，避免 WebUI 展示混乱。

### 验收

- 节点总流量、实时速度、重启后的历史累计能正常展示。

## 阶段 6：React WebUI 合并

建议分支：`feat/v2-react-webui`

完成标记: done

### 目标

引入 V2 的 React/Vite 前端，但适配当前主线 API 和功能。

### 实现

- 新增 `frontend/`。
- Dockerfile 增加前端构建阶段，并将 dist 复制到 `internal/monitor/assets/`。
- 先适配现有 API，再启用新增 API。
- 保留当前日志页、订阅配置页、手动 blacklist 操作。
- 删除 V2 中和当前后端不匹配的假设，例如默认端口、缺失的 `/api/subscription/config`。

### 验收

- `npm ci && npm run build` 通过。
- `go test ./...` 通过。
- Docker build 通过。
- WebUI 可完成登录、查看节点、探测、拉黑/释放、编辑配置、订阅刷新、查看日志。

## 阶段 7：配置、文档与迁移

建议分支：`docs/v2-migration-guide`

完成标记: done

### 任务

- 更新 `config.example.yaml`。
- 更新 README/README_ZH。
- 增加从纯文件模式迁移到 SQLite 模式的说明。
- 明确 Docker volume：
  - `./config.yaml`
  - `./nodes.txt`
  - `./logs`
  - `./data`

### 验收

- 新用户可按 README 从零启动。
- 老用户升级不丢节点、不丢配置。

## 阶段 8：回归与发布准备

建议分支：`chore/v2-release-readiness`

### 必跑检查

```bash
go test ./...
go test -race ./internal/config ./internal/monitor ./internal/store ./internal/subscription
npm ci --prefix frontend
npm run build --prefix frontend
docker build -t easy_proxies:merge-v2 .
```

### 手工场景

- 文件模式启动。
- store 模式启动。
- pool/multi-port/hybrid 三种模式。
- HTTP/SOCKS5/mixed 三种入口协议。
- 订阅刷新后 reload。
- 节点手动添加、禁用、删除、批量删除。
- GeoIP region route。
- 手动 blacklist 和 release。
- 日志写文件和 WebUI 日志查看。

## 不建议迁移的 V2 实现

- V2 的 `builder` 整体实现：会丢 TUIC、Hysteria2 port hopping 等当前能力。
- V2 的 `geoip` 整体实现：会丢 `sg`、DNS 缓存、Proxy-Authorization、transport 复用和独立路由端口。
- V2 的 `pool` 整体实现：会回退并发初始探测、手动 blacklist 等能力。
- V2 的 `cmd/main.go`：会移除当前日志轮转和启动重试。
- V2 的 Dockerfile 全量替换：路径、日志、compose 语义和当前项目不同。

## 推荐实施顺序

1. `feat/v2-baseline-audit`：基线确认。
2. `feat/v2-inbound-protocol`：入口协议配置。
3. `feat/v2-sqlite-store`：SQLite store 基础层。
4. `feat/v2-node-persistence`：节点持久化双轨适配。
5. `feat/v2-batch-node-api`：批量节点管理 API。
6. `feat/v2-traffic-stats`：运行统计与流量持久化。
7. `feat/v2-react-webui`：React WebUI。
8. `docs/v2-migration-guide`：文档与迁移说明。
9. `chore/v2-release-readiness`：回归与发布检查。

## 第一批可拆提交

1. `feat(config): support configurable inbound protocol`
2. `feat(store): add sqlite persistence layer`
3. `feat(nodes): persist manual nodes to optional store`
4. `feat(api): add batch node operations and import endpoint`
5. `feat(monitor): add per-node traffic summary stream`
6. `feat(frontend): add React dashboard build pipeline`
