# Changelog

本项目所有值得记录的变更都记在此文件。格式参考
[Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，
版本号遵循[语义化版本](https://semver.org/lang/zh-CN/)。

## [Unreleased]

## [0.6.0] - 2026-10-20

### Added

- **列表分页与两种密度**：`/api/sessions` 改为返回 `{sessions,total,limit,offset}`，列表按页追加并显示「加载更多（剩余 N）」；新增卡片 / 表格视图切换，表格把 agent、标题、项目、模型、消息、Token、花费、更新时间压缩到一张表里。排序新增「按花费」。
- **收藏与本地标签**：卡片和表格行可收藏，收藏项置顶；详情页可给会话加最多 8 个标签，卡片上显示 `#标签`。两者只存 localStorage，不写索引。项目侧栏支持按名称筛选，并可切换按会话数 / 名称 / 最近活跃排序；卡片目录旁可一键复制路径。
- **深链与浏览器历史**：`session` 进入查询串，详情可直接分享；`GET /api/sessions/{agent}/{id}/meta` 让不在当前页或筛选结果里的会话也能被深链打开。筛选、视图切换、打开/关闭详情使用 `pushState`，前进/后退能恢复。详情头部可直接复制会话 ID 和目录。
- **导出闭环**：详情支持下载 Markdown / JSON（不再只有复制）；列表/看板按当前筛选导出完整 CSV（分页拉取，最多 1 万条）。
- **对话详情阅读增强**：查找（`f`）并显示 `1/N` 命中、上/下一条用户消息（`p`/`n`）、宽屏、三档字号、长消息折叠、单条消息复制、点 `[ ]` 直接切换上/下一个会话。默认把 `reasoning` / `tool` / `system` 一起隐藏，打开开关才渲染。
- **详情操作栏重做**：按「阅读工具 / 复制与导出 / 关闭」分组，按钮改为带图标和文字的「查找、宽屏、字号、复制正文、导出 MD、导出 JSON、关闭」；宽屏与查找有激活态，字号按钮的 title 显示当前档位，移动端才显示「大纲」。
- **看板新增**：Top 会话（花费 / Token / 消息 / 压缩 / 工具调用）、工作节奏（按星期）、模型与工具面板的指标切换和「全部」展开、项目排行可展开全部、Agent / 项目表格点击表头排序、热力图「今天」标记与键盘操作、30 天范围。
- **工作节奏重做**：卡片移到「活动」下方，左侧近 7 天、右侧今天 0-23 点，两张细柱图并排，横轴刻度逐柱对齐，面板级切换消息 / 会话 / Token / 花费（偏好会记住）；新增 `activity_hour` 表与 `GET /api/hours`（按天+小时存储，Token / 花费按消息占比摊到小时），索引 schema 升到 v12（首次启动自动全量重建以填充小时数据）。
- **成本估算**：Codex / DSH 等没有花费字段的来源，按 `web/pricing.js` 的参考单价估算，UI 显示 `≈`；来源记录的真实花费始终优先。
- **自动刷新与扫描进度**：顶栏「自动刷新」开启后每 30 秒重新扫描日志、更新索引并刷新当前视图（默认关闭，局部刷新而非整页 reload）；`GET /api/scan` 暴露进度，首次扫描期间页面显示「正在扫描」而不是空白。启动顺序改为先监听、后台索引。
- **快捷键与帮助**：`j/k` 移动、`Enter` 打开、`[`/`]` 切换会话、`?` 查看帮助。
- `-rebuild` 强制全量重建缓存，`-open` 启动后打开浏览器；`GET /api/version` 与顶栏版本号。
- JSON / 静态资源支持 gzip；静态资源使用短缓存。
- 发布矩阵补充 `darwin/amd64`、`linux/arm64`，安装脚本同步。

### Changed

- 项目侧栏移除 agent 分布色点，恢复更宽松的项目名 / 会话数排版；`/api/projects` 仍保留 `agents` 字段供外部使用。

- **扫描容错**：单个数据源失败不再中止后续来源，`lastScan` 只在全部来源成功时更新，失败信息通过 `/api/scan` 的 `lastError` 暴露；文件库开放 4 个 SQLite 读连接（WAL），扫描写入时不再阻塞所有查询。
- **趋势分摊修正**：Token / 花费的分母改为会话全部活跃消息数，不再只统计 `days` 窗口内的行，跨年会话不会把历史用量全部摊到近期。
- **正文渲染**：默认过滤噪声后再渲染，按 120 条分块追加，Markdown 在块落盘时才解析；滚动大纲缓存偏移并二分查找，滚动事件用 rAF 节流。本机 8k+ 行会话不再一次性铺满 DOM。
- **Markdown**：嵌套列表支持到 5 层，`- [ ]` 任务列表渲染为复选框；图片默认显示占位、点击才加载（并带 `referrerpolicy="no-referrer"`），避免打开历史时向第三方泄露 IP。
- **顶栏**：Tokens 拆成输入 / 输出 / 缓存 / 花费四个可读胶囊；缓存命中率保留在 title 中。
- **模型统计**：`/api/models` 补充输入 / 输出 / 缓存读拆分，成本估算可用于模型面板。
- SQLite 文件库连接池从 1 调到 4；内存库仍保留单连接。

### Fixed

- **Claude Code 子代理未被识别**：新版 Claude Code 把子代理转录放在 `<编码路径>/<sessionId>/subagents/agent-<agentId>.jsonl`，扫描只看目录第一层 `.jsonl`，导致子代理会话完全没有索引、详情也没有父子关系。现在会扫描该子目录，以 agentId 作为会话 ID、`sessionId` 作为父会话记为 `subagent`，标题取同名 `.meta.json` 的 `description`，并且子代理详情不再因为整份转录都带 `isSidechain` 而被过滤成空。
- 升级后看板出现「卡片空白 / 按钮点击无反应」：静态资源此前以 `Cache-Control: public, max-age=600` 缓存，新页面会配上旧 JS / CSS。现在所有静态资源都要求每次重新验证（`no-cache`）。
- 详情页快速切换会话时旧响应覆盖新会话；导出/复制在加载期间会串到上一条会话；统计与看板请求增加请求序号，避免旧响应覆盖新筛选。
- 列表 500 条静默截断；搜索中的 `%` / `_` 现在按字面匹配（`LIKE ... ESCAPE`）。
- 热力图 tooltip 在选择「消息」时重复显示消息数；趋势与热力图时间范围不一致的说明补在 title 中。
- 详情 ID 未校验时，编码路径分隔符可能进入 Claude 的 fallback 文件查找；现在 API 层拒绝非 `[A-Za-z0-9._-]` 的 id。
- `POST /api/rescan` 要求 JSON Content-Type，跨站表单无法触发扫描；绑定非回环地址时启动日志打印警告。
- `lastScan` 在扫描失败时也被更新，导致 UI 显示「更新于刚刚」但实际失败。
- 卡片 `232.0k` 改为 `232k`；项目侧栏在窄宽度下更早截断的问题（为 agent 色点留出空间）。
- 看板 KPI 的涨跌颜色统一为「红涨绿跌」，不再对花费单独反色。
- 启动时列表偶发显示「没有匹配的会话」：扫描状态现在在 HTTP 监听前同步置位，前端把「索引尚未就绪」和「筛选无结果」区分开，并在有筛选时提供「清除筛选」按钮。
- 会话表格的模型列改为显示完整模型名（如 `claude-deepseek-v4.1-flash[1M]`），表格与卡片的 resume 按钮统一为「复制 resume」；窄窗口（≤1400px）隐藏项目列，把宽度留给模型和操作。
- 卡片有 compact / subagent 标签导致底部换行时，resume 按钮不再垂直居中，而是与第一行数字对齐。

## [0.5.0] - 2026-10-19

### Added

- **看板概览卡片**：顶部新增「今日 / 近 7 天 / 近 30 天」的 Token、消息数、花费及环比涨跌，以及当前范围的缓存命中率（缓存读 /（未缓存输入 + 缓存读））。
- **Agent 对比 / 项目排行改为可点击的对比表**：Agent 表列出会话、消息、输入、输出、缓存读、缓存命中率、花费与 Token 占比，当前 agent 高亮，点击行切换标签（再次点击回到全部）；项目表列出会话、消息、Token、花费、最近活跃与占比，点击行筛选该项目。
- **上下文压缩（compact）提示**：四个 agent 的压缩都能识别——Claude Code 的 `isCompactSummary` / `compact_boundary`、Codex 的 `compacted`、OpenCode 的 `compaction`（新格式消息与旧格式 compaction 模式回复，失败的不计）、DSH 的 `compaction/summary`。
  - 对话详情在压缩发生处显示分隔线（「上下文压缩 #N」+ 时间），压缩摘要默认折叠、展开按 Markdown 渲染；右侧大纲有对应条目，导出 Markdown 带「上下文压缩」小节；统计行显示「压缩 N 次」。
  - 会话卡片显示「compact ×N」标签（与 subagent 标签同款），次数写入索引，按来源文件累加。
- 对话详情的统计行拆分为输入 / 输出 / 缓存读，并显示缓存命中率；顶栏 Tokens 也显示当前范围的缓存命中率。
- **README 截图**：会话列表、统计看板、对话详情三张 WebP 截图（1600px 宽，各约 100KB）；`scripts/screenshots.mjs` 用合成的演示数据起服务并以无头 Chrome 重新生成，不读取本机真实会话。

### Changed

- **热力图与趋势合并为「活动」面板**：两者并排、共用指标与跨度切换；趋势裁掉开头的空白日（至少保留 14 天），跨度超过 92 天时按周聚合（「会话」按周取单日峰值），并加参考线、峰值与日期刻度；副标题按所选指标显示合计并标出峰值日。
- **看板布局**：模型分布与工具排行同排且都取前 8，高度对齐；项目排行是跨项目对比，选中单个项目时隐藏。
- 模型分布与项目排行先按 Token 排序再取前 N，柱长与「按 Token」一致；没有花费数据的 agent 显示「—」而非 `$0.00`。
- **会话卡片重排**为三层：顶部 agent / 模型 / 更新时间，中间标题 + 一行摘要 + 目录，底部（细线分隔）条数 / Token / 花费、subagent 与 compact 标签和 resume 按钮；0 tokens 不再显示。
- **对话详情头部**：元信息一行（agent 标签 · 目录 · 模型），统计改为分组的标签块，时间与会话 ID 单独一行；操作按钮浮在右上角，统计与子会话列表用满整行宽度。
- **Subagent 列表**：由一串胶囊按钮改为可折叠列表，每个子会话一行（序号、完整标题、条数与 Token），超过 2 个时默认折叠为一行摘要，超过 4 行内部滚动；子会话页面显示「↩ 主会话」返回入口。
- **压缩摘要**改为醒目的折叠卡片：收起时显示摘要开头与「展开摘要 · N 字」按钮。
- Claude 的输入 Token 计入 `cache_creation_input_tokens`（缓存写入同属未命中缓存的输入；部分网关下 `input_tokens` 恒为 0、全部记在这里），Claude 的输入总量与缓存命中率会随之变化。
- 索引 schema 升至 v10，升级后首次启动会全量重扫一次，以修正已缓存的 Token 数并补齐压缩次数。

### Fixed

- **Claude Token 重复计数**：Claude Code 把一次响应的每个 content block 写成一行并重复同一份 `usage`，现按 `message.id` 去重。
- **Codex 缓存重复计数**：OpenAI 的 `input_tokens` 已包含 `cached_input_tokens`，现只存未缓存部分，与其他 agent 口径一致。
- 长对话（300 条以上）点击右侧大纲无法精确定位、滚动条反复抽搐：去掉消息上的 `content-visibility: auto`，其占位高度在平滑滚动途中被真实高度替换，导致目标位移并与滚动锚定互相拉扯。
- Claude Code 的压缩摘要不再被当成一条用户消息显示。
- 搜索 / 日期 / 项目筛选下，会话卡片的「subagent ×N」标签消失：子会话数改为按全局关联关系统计，不再只数当前列表里命中的子会话。

## [0.4.0] - 2026-10-19

### Added

- **DeepSeek Harness（DSH）支持**：新增 `dsh` 数据源，读取 `~/.dsh/sessions`（设置 `DSH_HOME` 时读取 `$DSH_HOME/sessions`）下的追加式会话日志，默认 `-dsh-dir` 可覆盖。
  - 兼容 DSH 的 `.zstd` 压缩日志与未压缩 `.jsonl`；压缩日志按「头帧 + 每个追加批次一帧」做帧级增量解析，只解压新增帧，并能从崩溃留下的尾部残帧恢复。
  - 标题取 `session/title`，模型取 `request/context` / assistant 消息，Token 取 `assistant/message.usage`，工具调用取 `tool/call` 并归一化；只统计真实用户消息（忽略注入的 runtime-context / skill-catalog 等上下文）。
  - `parentSession` + `origin: subagent` 解析为子代理关系；详情页展示文本、思考与工具调用。
  - 前端新增 DSH 标签与配色；DSH 没有稳定的命令行恢复入口，因此不显示 resume 按钮。

### Changed

- **发布说明自动化**：`release.yml` 改为用 `scripts/release-notes.sh` 从 `CHANGELOG.md` 抽取当前 tag 的版本段落作为 Release 正文，并附 `**Full changelog:** <上一个稳定 tag>...<当前 tag>` 的 compare 链接（rc tag 若还没有自己的段落则回退到 `Unreleased`）。

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

[Unreleased]: https://github.com/AkisAya/agent-session-viewer/compare/v0.6.0...HEAD
[0.6.0]: https://github.com/AkisAya/agent-session-viewer/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/AkisAya/agent-session-viewer/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/AkisAya/agent-session-viewer/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/AkisAya/agent-session-viewer/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/AkisAya/agent-session-viewer/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/AkisAya/agent-session-viewer/releases/tag/v0.1.0
