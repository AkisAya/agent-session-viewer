# Changelog

本项目所有值得记录的变更都记在此文件。格式参考
[Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，
版本号遵循[语义化版本](https://semver.org/lang/zh-CN/)。

## [Unreleased]

### Added

- **js/wasm 端到端对拍**：`internal/wasmtest` 用一份固定固件（Claude / Codex / OpenCode 三个来源，含跨 rollout 文件的续接会话、子代理与分叉）跑完整流水线并把结果渲染成稳定文本，与 `testdata/golden.txt` 逐字节比对；同一份测试在原生与 wasm 下各跑一次，CI 新增 `wasm` 作业执行后者。
- **浏览器 harness**：`internal/wasmtest/browser/` 用 `vfs.js` 把内存文件系统装到 `globalThis.fs`（在 `wasm_exec.js` 之前），于是 Go 侧零改动即可在浏览器里跑真实扫描与查询；页面把输出与 golden 对比并显示结论。`build.sh` 负责组装，`dist/` 不入库。
- **`internal/sqlite`**：按平台选择 SQLite 驱动——原生 `modernc.org/sqlite` 不变，wasm 用 `ncruces/go-sqlite3`（wazero 里跑官方 SQLite）。同时收编「打开数据库」这一唯一不可移植的操作：原生按路径只读打开，wasm 把文件字节挂进内存 VFS。
- `cmd/wasmcheck`：手动跑一遍完整流水线并打印结果与耗时，原生 / Node / 浏览器通用，默认自带固件、`-home` 可指向真实数据目录。

### Changed

- **发布说明自动化**：`release.yml` 改为用 `scripts/release-notes.sh` 从 `CHANGELOG.md` 抽取当前 tag 的版本段落作为 Release 正文，并附 `**Full changelog:** <上一个稳定 tag>...<当前 tag>` 的 compare 链接（rc tag 若还没有自己的段落则回退到 `Unreleased`）。
- **wasm 下索引强制内存**：js/wasm 没有 mmap，SQLite 的文件 VFS 无法打开任何数据库，因此 `index.Open` 在 wasm 下忽略 `Path`，一律使用内存库。

## [0.3.0] - 2026-10-17

### Added

- `-version` 参数，发布产物通过 `-ldflags -X main.version=...` 注入版本号。
- **测试护栏**：「全量解析 == 逐次增量解析」的等价测试，覆盖 Claude 与 Codex 解析器，附带多文件会话、跨 agent 关联、各 parser 的 Transcript 等用例；CI 工作流执行 `gofmt` / `go vet` / `go test -race`。
- **多平台发布**：发布 macOS arm64、Linux amd64 与 Windows amd64 产物，`CGO_ENABLED=0` 纯静态构建（Windows 为 `.exe`，打包为 `.zip`）。带 `-` 的 tag（如 `v0.3.0-rc1`）自动标记为 prerelease，不会占据 "latest"。
- **前端错误提示**：请求失败时在底部弹出提示（优先展示服务端返回的 `error` 文本），不再静默失败；看板单个面板失败时保留上一次数据，不再整体清空。
- **前端资产护栏**：测试校验 `index.html` 引用的资源与各模块的相对 `import` 均存在于 `//go:embed` 的 `web.FS` 中，避免漏加文件只在浏览器里才报错。
- **前端冒烟测试**：`web/smoke.mjs` 用桩 DOM 在 node 里加载真实模块图，跑通启动流程与列表 / 热力图 / 对话详情（含 markdown）等渲染路径；CI 新增 `frontend` 作业执行它。

### Changed

- **默认仅本机监听**：HTTP 默认绑定 `127.0.0.1`，新增 `-host` 参数（本工具会暴露全部本机对话记录，不应默认对外开放）。
- **Delta 模型重构**：用 `Counter` / `Usage` / `Amount` / `Histogram` 四个带语义的类型取代原先散落的多个 `*Absolute` 布尔标志，来源只需声明「整文件覆盖」或「增量累加」。
- **按来源累计用量**：消息数 / Token / 花费改为按 `(agent, 会话, 来源文件)` 存于 `session_usage`，会话行由各来源求和；修复横跨多个 rollout 文件的 Codex 续接会话 token 被后一个文件覆盖的问题。
- **`index` 包拆分**：930 行的单文件拆为生命周期/扫描、写侧、读侧三部分。
- **前端拆分**：1239 行的单文件 `app.js` 拆成原生 ES 模块——`util`（格式化/转义/剪贴板/提示）、`state`（状态、URL 同步、主题）、`api`（HTTP 客户端，失败时抛出可展示的错误）与 `list` / `dashboard` / `projects` / `stats` / `detail` 视图模块；仍是零构建，由 Go 直接内嵌分发。
- **性能**：正文用索引记录的来源路径直接定位并逐行流式读取；写侧改用预编译语句与单条 upsert；OpenCode 在会话表未变化时跳过全部逐消息扫描。
- **关联会话匹配**：父/子匹配改为按 `(agent, id)` 成对比较，避免不同 agent 复用同一 id 时串链。
- **Windows 支持**：本工具自己的索引数据库在 Windows 上默认落到 `%LOCALAPPDATA%\agent-session-viewer\index.db`（其他平台仍是 `~/.local/share/...`）；三个 agent 的历史路径本就与平台无关（`~/.claude`、`~/.codex`、`~/.local/share/opencode`），未作改动。

### Fixed

- 增量解析不再用后追加的用户消息覆盖会话摘要 `preview`（摘要只在该来源文件首次全量解析时写入）。
- 会话清理改为按「来源文件成员关系」判断：一个 Codex 会话横跨多个 rollout 文件时，删除其中一个不再误删整个会话。

### Removed

- 移除仅构建 macOS arm64 的 `release-macos.yml`，由多平台 `release.yml` 取代。

## [0.2.0] - 2026-10-17

### Added

- **统计看板**：新增「会话 / 看板」视图切换，看板随当前 agent 标签与项目筛选联动。
  - 使用热力图：GitHub 风格色阶，可切换指标（消息 / 会话 / Token / 花费）与跨度（90 / 180 / 365 天），
    带色阶图例；点击某天可把列表筛选到当天，列表顶部显示日期小标签并支持一键清除；附活跃天数与最长连续天数。
  - 趋势图：按本地日期的消息 / 会话 / Token / 花费趋势，Token 与花费按会话每日活跃度近似分摊。
  - 模型分布、项目排行、工具排行（含次数与涉及会话数）、Agent 对比面板。
- **作用域筛选**：`/api/stats` 支持 `agent` / `cwd` / `day`，并返回输入 / 输出 / 缓存读的 Token 拆分与 `lastScan`；
  `/api/sessions` 新增 `day` 过滤（只返回该本地日期有活动的会话）。
- **顶栏统计联动**：会话数 / Token / 花费跟随当前 agent、项目、日期筛选变化，
  Token 拆分为输入 `↑` / 输出 `↓` / 缓存读 `⚡`，并显示「更新于 X 分钟前」。
- **对话 Markdown 渲染**：标题 / 列表 / 表格 / 引用 / 代码块（带语言标签与复制按钮）/ 链接与图片
  （仅放行 `http(s)`、`mailto`，其余按纯文本转义）。
- **消息骨架导航**：右侧大纲栏按顺序列出可见消息，点击跳转，阅读位置变化时自动高亮当前消息。
- **导出对话**：详情页「导出」按钮把整段对话复制为 Markdown，消息带时间戳并按天分隔。
- **关联会话**：支持 `subagent` / `fork` / `resume` 关系。subagent 会话不再单独出现在列表中，
  从主会话详情页的关联链接打开，详情页内点标记可跳回主会话；
  OpenCode 的 `parent_id` / `fork_session_id`、Codex 的 `parent_thread_id` 均解析为父子关系。
- **resume 按钮**：每张卡片带 `⧉ resume`，一键复制恢复命令
  （`claude --resume <id>` / `codex resume <id>` / `opencode --session <id>`）。
- **URL 状态**：agent / 项目 / 视图 / 排序 / 搜索 / 日期写入查询参数，可分享收藏，浏览器前进后退可用。
- **工具名归一化**（`internal/source/toolname.go`）：把三个 agent 的不同工具命名
  （Claude `Bash`、Codex `exec_command`、OpenCode `shell`/`bash`）统一映射到规范名，
  MCP 工具保留为 `mcp:server.tool`；附带单元测试。
- **活动 / 工具直方图**：以 `(agent, 会话, 来源文件)` 为主键存储按天消息数与工具调用次数，
  支撑热力图、趋势与工具排行。
- 新 API：`GET /api/activity`、`GET /api/trend`、`GET /api/models`、`GET /api/tools`、`GET /api/relations`。
- 无障碍与交互：卡片 / 项目行支持键盘聚焦（Enter / Space 激活），尊重系统 `prefers-reduced-motion`，
  长对话采用按需渲染（`content-visibility`）。

### Changed

- 概览卡片新增**模型**展示，主会话卡片带 `subagent ×N` 标记。
- 过长的目录路径按头尾保留、中间省略，完整路径悬浮可见。
- 项目排序确定化：会话数降序 → 更新时间降序 → 目录名。
- 解析器增强：
  - Claude Code：从 `tool_use` 块统计工具调用，按行内 `timestamp` 归日。
  - Codex：工具调用优先取 `event_msg` 的 `CommandExecution` / `WebSearch` / `McpToolCall`，
    否则取 `response_item` 的 `function_call` / `custom_tool_call`；两条消息流分别累计后择一，避免重复计数。
  - OpenCode：按 `time_created` 归日，统计 assistant 消息中的 `tool` 内容块。
- 依赖整理：`go mod tidy`，将 `modernc.org/sqlite` 标记为直接依赖。
- README 增补数据来源、命令行参数、设计要点与 API 表格。

### Removed

- 列表视图不再额外堆叠「分叉 / 续接关系」面板与项目头部统计面板，只展示会话卡片。

## [0.1.0] - 2026-10-17

首个发布版本。

### Added

- 单个 Go 二进制、内嵌前端页面，用于浏览本机 **Claude Code**、**Codex**、**OpenCode** 的历史对话记录。
- **增量索引**：SQLite 缓存为每个源文件记录 `size` / `mtime` / 解析偏移量，
  未变化时整文件跳过，有追加时只解析新增字节，二次启动近乎瞬时。
- **按需加载正文**：列表只读缓存，对话内容在点击会话时才从源文件读取。
- **稳定 ID**：解析出的 session id 随游标一起持久化，保证增量扫描不会因读不到文件头而错配。
- **只读访问**：读取 OpenCode 数据库与磁盘文件均为只读，不影响正在运行的 agent。
- 顶部标签切换：全部 / Claude / Codex / OpenCode，各标签带会话数量。
- 主题切换：跟随系统 / 浅色 / 深色（记住选择，无刷新闪烁）。
- 左侧项目目录侧边栏：按工作目录查看会话，窄屏自动切换为下拉框。
- 概览卡片：按 agent 配色的左侧色条、标题、首条用户消息摘要、消息数、Token、花费、更新时间。
- 搜索（标题 / 目录 / 内容摘要，匹配项高亮）、排序（更新时间 / 创建时间 / 消息数 / Token）。
- 点击卡片查看完整对话，可折叠「思考 / 工具」内容。
- 命令行参数：`-port`、`-db`、`-claude-dir`、`-codex-dir`、`-codex-index`、`-opencode-db`、`-watch-interval`。
- `-memory` 模式：索引只放内存（`mode=memory&cache=shared`），不落盘、不需要 `-db` 目录。
- 「刷新」按钮触发一次增量扫描。
- macOS（arm64）发布工作流，推送 `v*` tag 时自动构建并上传产物。

[Unreleased]: https://github.com/AkisAya/agent-session-viewer/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/AkisAya/agent-session-viewer/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/AkisAya/agent-session-viewer/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/AkisAya/agent-session-viewer/releases/tag/v0.1.0
