# 数据库持久化、订阅管理与节点编辑计划

## 当前状态

状态：已完成并合入 `dev`。

本轮改造已废弃 `config.yaml` 和 `nodes.txt` 作为运行配置来源。SQLite 是唯一持久化来源；代码默认值只用于首次启动和缺省字段补齐。启动流程为：内置默认值 -> SQLite 运行设置/节点/订阅/会话 -> 环境变量覆盖 -> 构建 sing-box 配置。

环境变量只作为运行时覆盖项：

- `MANAGEMENT_PORT` 覆盖管理端 WebUI/API 端口。
- `MANAGEMENT_PASSWORD` 只从进程或容器环境变量读取，不入库、不出现在 API 响应、前端表单或日志中。

容器内数据库固定为 `/app/data/data.db`，本地开发默认使用 `data/data.db`，也可通过 `--database` 指定。

## 已完成范围

- 数据库唯一持久化：运行设置、节点、订阅源、订阅刷新状态、会话、端口、禁用状态和流量统计都写入 SQLite。
- 无文件启动：删除 `config.yaml` 和 `nodes.txt` 后可正常启动，并且不会重新创建这两个文件。
- 订阅管理：订阅已做成独立菜单，支持多个订阅、启用/禁用、自动更新开关、刷新间隔、手动刷新和状态展示。
- 节点共存：手工节点和订阅节点同时显示在节点管理中，并一起参与 `pool`、`multi-port`、`hybrid` 三种模式。
- 来源语义：WebUI/API 创建或导入的节点为 `manual`；订阅刷新写入 `subscription` 节点并保留 `subscription_id`。
- 订阅节点保护：订阅节点支持查看、启停和探测，但不支持编辑或删除。
- 结构化节点编辑：节点弹窗提供“出站”和“入站”页；出站页左侧表单、右侧 JSON 实时同步，URI 通过“解析”按钮导入并覆盖 JSON。
- sing-box 字段约束：表单只暴露当前 builder 明确支持的出站字段，协议切换、TLS 开关和传输类型切换会清理不再适用的子字段。
- 弹窗交互：点击弹窗外或按回车不会误关闭，只有保存/取消/关闭按钮会结束弹窗。

## 验收结果

已执行：

```bash
npm --prefix frontend run lint
npm --prefix frontend run build
go test ./...
```

Docker 真实流程已验证：

- 项目使用 Docker host 网络启动。
- 另起 `serjs/go-socks5-proxy` 容器作为真实 SOCKS 上游节点。
- 通过 WebUI/API 添加手工节点和订阅源，并刷新出订阅节点。
- 使用 `curl` 分别验证 `pool`、`multi-port`、`hybrid` 的 HTTP 与 SOCKS 入口连通。
- 使用 Playwright 验证节点管理、订阅管理、订阅节点只读、VLESS 表单与 JSON 双向同步。
- 重启容器后验证设置、节点、订阅源和代理入口仍然可用。

最终通过输出：

```text
final docker proxy modes e2e passed
final docker ui restart e2e passed
```

## 后续建议

- 增加可提交的 Playwright E2E 测试脚本，避免继续依赖临时 inline 脚本。
- 清理历史计划文档中关于 legacy 文件模式的旧描述，或明确标记为历史记录。
- 继续补充结构化出站表单字段，前提是 sing-box 文档和当前 builder 都明确支持。
