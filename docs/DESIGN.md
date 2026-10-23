# 设计说明

本文面向维护者，说明内部实现与数据解析细节。使用方式见 [README](../README.md)。

## 数据源解析细节

| Agent | 路径 | 解析要点 |
|---|---|---|
| Claude Code | `~/.claude/projects/<编码路径>/<sessionId>.jsonl` | 标题取 `custom-title` / `ai-title` / `last-prompt`，费用取 `cost-state`，思考取 `thinking` 块，工具调用取 `tool_use` 块，活动按行内 `timestamp` 归日；子代理转录在同目录 `<sessionId>/subagents/agent-<agentId>.jsonl`，其中的 `sessionId` 指向父会话、所有行都是 `isSidechain`，因此以文件名中的 agentId 为会话标识，标题取同名 `.meta.json` 的 `description`，并记为父会话的 `subagent` |
| Codex | `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl` | 标题优先取 `session_index.jsonl`，模型取 `turn_context`，Token 取累计 `token_count`，回退解析 `response_item`；工具调用优先取 `event_msg` 的 `CommandExecution`/`WebSearch`/`McpToolCall`，否则取 `response_item` 的 `function_call`/`custom_tool_call`；`parent_thread_id` 非自身时记为续接 |
| OpenCode | `~/.local/share/opencode/opencode.db` | 只读打开，读取 `session_v2` 与 `session_message`（兼容旧的 `message`/`part`）；活动按 `time_created` 归日，工具调用取 assistant 消息中的 `tool` 内容块；`parent_id` 记为子代理（`subagent`），`fork_session_id` 记为分叉（`fork`） |
| DSH | `~/.dsh/sessions/<编码 cwd>/<sessionId>/session.vN.jsonl.zstd` | 读取 DSH 的追加式会话日志；`session.vN.jsonl`（未压缩）同样支持。标题取 `session/title`，模型取 `request/context` / assistant 消息，Token 取每条 `assistant/message` 的 `usage`（输入 / 输出 / 缓存读）；真实用户消息只认 `source.kind = user`，忽略注入的 runtime-context / skill-catalog 等上下文；工具调用取 `tool/call`；`parentSession` + `origin: subagent` 记为子代理 |

## 设计要点

- **增量索引**：缓存数据库（SQLite）为每个源文件记录 `size` / `mtime` / 解析偏移量。文件未变化时整文件跳过；有追加时只解析新增字节并累加统计。
- **启动即服务**：HTTP 监听先启动，首次索引在后台进行；`StartScan` 会在监听前同步置位 `scanning`，所以首次打开页面看到的是「正在加载/扫描中」而不是空列表。`/api/scan` 暴露扫描进度与上次错误。单个数据源失败不会中止其他来源，`lastScan` 只在全部来源成功时更新。
- **列表分页**：`/api/sessions` 返回 `{sessions,total,limit,offset}`，默认每页 100（上限 1000），前端按页追加；支持 `updated / created / messages / tokens / cost / compacts` 排序。子代理转录只在 `subagents=1` 时作为独立行返回，默认隐藏且不计入任何会话数（父会话卡片用「subagent ×N」标记）。
- **正文按需 + 分块渲染**：列表只读缓存，对话内容在点击会话时才读取。详情默认不渲染 reasoning / tool / system，可见消息按 120 条一块追加，Markdown 只在块真正落盘时解析；滚动大纲使用缓存偏移和二分查找，避免长会话卡顿。
- **可分享的浏览状态**：`agent / cwd / day / sort / view / session` 写入查询串；筛选、切换视图、打开/关闭详情使用 `pushState`，浏览器前进/后退可恢复，详情可直接分享。
- **本地个人化**：收藏、标签、主题、卡片/表格、宽屏/字号、自动刷新开关等偏好保存在 localStorage，不写入索引，也不上传。
- **自动刷新与 `-watch-interval`**：顶栏开关每 30 秒调用 `/api/rescan`（增量扫描日志、更新索引），扫描完成后由 `/api/scan` 的 `lastScan` 变化触发当前页面重新加载数据；这是局部刷新，不是整页 reload。`-watch-interval` 由服务端定时扫描，页面同样通过 `/api/scan` 自动跟随。两者通常二选一：页面常开时用顶栏；需要在浏览器关闭后仍保持索引新鲜（或多标签、外部 API）时用 `-watch-interval`。同时开启不会并发写索引，但客户端定时请求是多余的。
- **费用**：来源记录了花费时直接用真实值；没有记录时按 `web/pricing.js` 的参考单价估算并在 UI 显示 `≈`，仅作参考。
- **传输**：JSON 与静态资源在客户端支持时使用 gzip；带内容哈希的构建不在本项目范围内，静态资源使用短缓存。
- **只读访问**：读取 OpenCode 数据库与磁盘文件均为只读，不影响正在运行的 agent。
- **默认仅本机可访问**：HTTP 默认绑定 `127.0.0.1`。本工具会暴露全部本机对话记录，因此只有显式传 `-host 0.0.0.0` 才会对外网开放；非回环地址会在启动日志中打印警告。
- **会话 ID 校验**：详情接口只接受 `[A-Za-z0-9._-]` 组成的 id，防止编码路径分隔符进入各来源的 fallback 文件查找。
- **重扫接口**：`POST /api/rescan` 要求 `Content-Type: application/json`，避免跨站表单触发扫描；扫描异步进行，前端轮询 `/api/scan`。
- **DSH 帧级增量**：DSH 的 `.zstd` 日志是「头帧 + 每个追加批次一帧」的独立 Zstandard 帧拼接。解析器按帧边界推进游标，只解压新增帧；崩溃留下的尾部残帧会被保留到下次扫描，不会误当成损坏文件。
- **稳定 ID**：解析出的 session id 随游标一起持久化，保证增量扫描时不会因为读不到文件头而错配。
- **内存模式**：`-memory` 用内存版 SQLite（`mode=memory&cache=shared`）承载同一套索引，不落盘、不需要 `-db` 目录；代价是没有持久化游标，每次启动都要全量扫描一次。
- **按来源累计用量**：消息数 / Token / 花费以 `(agent, 会话, 来源文件)` 为主键存于 `session_usage`，会话行由各来源求和得出，因此一个横跨多个 rollout 文件的 Codex 续接会话会正确累加而不是相互覆盖。每个来源单独记录「整文件解析 = 覆盖」还是「增量解析 = 累加」，避免手工组合多个绝对/相对标志。
- **活动 / 工具直方图**：`activity`（按天消息数）与 `tool_use`（按工具调用次数）同样以 `(agent, 会话, 来源文件)` 为主键存储。整文件解析视为该来源文件的绝对值（直接替换），增量解析只累加新增行——因为一个 Codex 会话可能横跨多个 rollout 文件，统计必须落到来源文件维度，否则后解析的文件会覆盖先前的统计。来源文件消失且会话再无其他来源时一并清理。
- **来源提示**：对话内容按索引里记录的来源路径直接定位（回退到目录扫描），逐行流式解析，不进常驻内存。
- **运行状态**：`/api/live` 每次请求直接读取 agent 自己写的进程状态文件，不进索引、不触发扫描，前端在页面可见时每 5 秒轮询，结果用于卡片 / 表格行上的状态标签和顶栏的数量统计（同一会话开在两个终端时按更紧急的状态只计一次），状态变化只原地替换标签（不重绘列表，焦点与滚动位置不变）。Claude Code 为每个运行中进程写 `<配置目录>/sessions/<pid>.json`，含 `sessionId`、`cwd`、`status`（`busy` / `shell` / `waiting` / `idle`）、`waitingFor`（等待原因，如 `dialog open`）与 `statusUpdatedAt`。这是 Claude Code 的内部格式，字段一律按可选处理，解析失败的文件（例如恰好在写入中）跳过、下次轮询再读。崩溃进程会留下状态文件，pid 也会被复用：先用文件名里的 pid 做存活检查（Unix `kill(pid, 0)`，Windows `OpenProcess` + 退出码），死进程的文件不打开；macOS 上再用 sysctl 取进程启动时间，与文件里的 `procStart`（Claude Code 以 `TZ=UTC` 运行 `ps -o lstart=` 得到）比对，相差超过 2 秒视为 pid 复用。Linux / Windows 暂只做存活检查。同目录的 `<pid>.<hash>.key` 是密钥文件，从不读取。实测 7 个运行中会话一次约 2ms，残留文件按文件名过滤后每个约 1.5µs，开销取决于运行中的进程数，与历史会话数量无关。
- **工具名归一化**：四个 agent 的工具命名各不相同（Claude 的 `Bash`、Codex 的 `exec_command`、OpenCode 的 `shell`/`bash`、DSH 的 `bash`），在解析阶段统一映射到规范名，MCP 工具保留为 `mcp:server.tool`。

## 项目结构

```
main.go                 CLI 入口
internal/config         路径与参数解析
internal/model          归一化的 Session / Message / Stats
internal/source         数据源接口与增量 Delta 定义
internal/source/claude  Claude Code 解析
internal/source/codex   Codex 解析（跨 rollout 文件合并对话）
internal/source/opencode OpenCode SQLite 解析
internal/source/dsh     DSH 会话日志解析（zstd 帧级增量）
internal/proc           进程存活与启动时间检查（运行状态用，单次 syscall，不 exec ps）
internal/index          缓存数据库 + 增量扫描编排（index.go 生命周期/扫描，store.go 写侧，query.go 读侧 API，live.go 运行状态汇总）
internal/api            HTTP API
web                     内嵌前端：原生 ES 模块（无构建），入口 app.js，state/api/util/pricing 加 list/dashboard/projects/stats/detail/live 视图模块
web/smoke.mjs           前端冒烟测试（node，桩 DOM 加载真实模块图，不参与内嵌）
scripts/release-notes.sh 发布说明：从 CHANGELOG.md 抽取当前 tag 的段落，附与上一个稳定 tag 的 compare 链接
scripts/screenshots.mjs  README 截图：生成合成会话数据，起服务并用无头 Chrome 截图到 docs/screenshots
```

## HTTP API

`internal/api` 暴露的接口，内嵌前端与外部工具均使用：

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/version` | 返回二进制版本（`dev` 表示未注入版本号） |
| GET | `/api/scan` | 当前扫描状态：`scanning`、`source`、`done`/`total`、`lastScan`、`lastError` |
| GET | `/api/stats?agent=&cwd=&day=` | 会话数 / 消息数 / Token / 花费汇总；`agents` 为按目录统计的各 agent 分布（供标签页），`total` 随 agent + 目录 + 日期筛选 |
| GET | `/api/projects?agent=` | 按工作目录聚合的会话数 / 消息数 / Token / 花费 / 各 agent 分布，按会话数降序 |
| GET | `/api/sessions?agent=&cwd=&q=&day=&sort=&limit=&offset=&subagents=` | 会话概览分页；返回 `{sessions,total,limit,offset}`，每行含 `toolCount`（该会话的工具调用次数）；`sort` 支持 `updated` / `created` / `messages` / `tokens` / `cost` / `compacts`；`day` 只返回该本地日期有活动的会话；`subagents=1` 时把子代理会话也作为独立行返回 |
| GET | `/api/sessions/{agent}/{id}` | 单个会话的完整对话；id 仅接受 `[A-Za-z0-9._-]` |
| GET | `/api/sessions/{agent}/{id}/meta` | 单个会话的缓存概要；供深链在筛选结果之外打开会话 |
| GET | `/api/activity?agent=&cwd=` | 按本地日期聚合的消息数与会话数（仅 API；看板热力图改用 `/api/trend`） |
| GET | `/api/trend?agent=&cwd=&days=` | 按本地日期的消息 / 会话 / Token / 花费趋势；Token 与花费按会话全部活跃消息数分摊（分母不受 `days` 截断） |
| GET | `/api/models?agent=&cwd=` | 按模型聚合的会话数 / 消息数 / Token（含输入 / 输出 / 缓存读）/ 花费 |
| GET | `/api/tools?agent=&cwd=&limit=` | 工具调用排行及未截断的总调用次数 |
| GET | `/api/relations?agent=&cwd=` | 处于父子关系中的会话（含父与子），供详情页显示并跳转到主会话 / subagent / 分叉 / 续接的关联会话 |
| GET | `/api/live` | 正在运行的会话：`{sessions:[{agent,id,pid,cwd,status,waitingFor,since,session}]}`，按待处理 → 运行中 → 等待输入排序，同一状态内最近变化的在前；`session` 是已索引的会话概要，尚未被扫描到时缺省。每次请求实时读取进程状态文件（`Cache-Control: no-store`）；某个来源读取失败时在 `error` 中说明，其余来源照常返回 |
| POST | `/api/rescan` | 触发异步增量扫描；要求 `Content-Type: application/json`，通过 `/api/scan` 轮询进度 |

## 扩展新数据源

1. 在 `internal/source/<agent>` 实现数据源接口，产出 `Delta`（整文件解析或增量解析）。
2. 在 `internal/index` 注册来源；统计一律落到 `(agent, 会话, 来源文件)`。
3. 在 `internal/api` 与前端视图（`web/`）中补充对应的 agent 标识与展示。
4. 会话 id 需匹配 `[A-Za-z0-9._-]`（详情接口的路径安全校验），否则深链和详情页无法打开。
5. 可选：实现 `source.Live` 接口（返回运行中的会话及其状态）即可在卡片上显示运行状态；它在每次 `/api/live` 请求时调用，必须足够轻量，状态目录不存在时返回空而不是错误。
