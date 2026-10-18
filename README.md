# Agent Session Viewer

一个轻量的本地工具，用于浏览本机上 **Claude Code**、**Codex**、**OpenCode** 和 **DeepSeek Harness（DSH）** 的历史对话记录。

- 单个 Go 二进制，内嵌前端页面
- 增量索引：只扫描新增内容，二次启动几乎瞬时
- 空闲低占用，可选主动轮询/手动刷新来更新信息

## 快速开始

```bash
go build -o asv .
./asv            # 默认：索引持久化到 SQLite 缓存
./asv -memory    # 可选：索引只放内存，不创建/读写缓存数据库
```

打开 http://localhost:8787

## 数据来源

| Agent | 路径 | 说明 |
|---|---|---|
| Claude Code | `~/.claude/projects/<编码路径>/<sessionId>.jsonl` | 标题取 `custom-title` / `ai-title` / `last-prompt`，费用取 `cost-state`，思考取 `thinking` 块，工具调用取 `tool_use` 块，活动按行内 `timestamp` 归日 |
| Codex | `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl` | 标题优先取 `session_index.jsonl`，模型取 `turn_context`，Token 取累计 `token_count`，回退解析 `response_item`；工具调用优先取 `event_msg` 的 `CommandExecution`/`WebSearch`/`McpToolCall`，否则取 `response_item` 的 `function_call`/`custom_tool_call`；`parent_thread_id` 非自身时记为续接 |
| OpenCode | `~/.local/share/opencode/opencode.db` | 只读打开，读取 `session_v2` 与 `session_message`（兼容旧的 `message`/`part`）；活动按 `time_created` 归日，工具调用取 assistant 消息中的 `tool` 内容块；`parent_id` 记为子代理（`subagent`），`fork_session_id` 记为分叉（`fork`） |
| DSH | `~/.dsh/sessions/<编码 cwd>/<sessionId>/session.vN.jsonl.zstd` | 读取 DSH 的追加式会话日志；`session.vN.jsonl`（未压缩）同样支持。标题取 `session/title`，模型取 `request/context` / assistant 消息，Token 取每条 `assistant/message` 的 `usage`（输入 / 输出 / 缓存读）；真实用户消息只认 `source.kind = user`，忽略注入的 runtime-context / skill-catalog 等上下文；工具调用取 `tool/call`；`parentSession` + `origin: subagent` 记为子代理 |

四个 agent 的历史路径在各平台一致：Windows 上 `~` 即 `%USERPROFILE%`，OpenCode 也沿用 Unix 风格的 `~/.local/share/opencode`。DSH 默认读取 `~/.dsh/sessions`，设置了 `DSH_HOME` 时改为 `$DSH_HOME/sessions`。只有本工具自己的索引数据库遵循平台习惯——Windows 下默认 `%LOCALAPPDATA%\agent-session-viewer\index.db`，其他平台 `~/.local/share/agent-session-viewer/index.db`（`-db` 可覆盖）。

## 命令行参数

```
-host string           HTTP 绑定地址（默认 127.0.0.1；设为 0.0.0.0 才对外网开放）
-port int              HTTP 端口（默认 8787）
-db string             缓存数据库路径（默认 ~/.local/share/agent-session-viewer/index.db；Windows 下为 %LOCALAPPDATA%\agent-session-viewer\index.db；-memory 时忽略）
-claude-dir string     Claude projects 目录
-codex-dir string      Codex sessions 目录
-codex-index string    Codex session_index.jsonl 路径
-opencode-db string    OpenCode 数据库路径
-dsh-dir string        DSH sessions 目录（默认 ~/.dsh/sessions，遵循 DSH_HOME）
-watch-interval int    每隔 N 秒自动增量扫描（0 = 仅按需，默认）
-memory                索引只保存在内存中，不创建也不读写缓存数据库（每次启动全量扫描）
-version               打印版本号后退出
```

## 设计要点

- **增量索引**：缓存数据库（SQLite）为每个源文件记录 `size` / `mtime` / 解析偏移量。文件未变化时整文件跳过；有追加时只解析新增字节并累加统计。
- **DSH 帧级增量**：DSH 的 `.zstd` 日志是「头帧 + 每个追加批次一帧」的独立 Zstandard 帧拼接。解析器按帧边界推进游标，只解压新增帧；崩溃留下的尾部残帧会被保留到下次扫描，不会误当成损坏文件。
- **按需加载正文**：列表只读缓存，对话内容在点击某个会话时才从源文件读取，不进常驻内存。
- **稳定 ID**：解析出的 session id 随游标一起持久化，保证增量扫描时不会因为读不到文件头而错配。
- **只读访问**：读取 OpenCode 数据库与磁盘文件均为只读，不影响正在运行的 agent。
- **默认仅本机可访问**：HTTP 默认绑定 `127.0.0.1`。本工具会暴露全部本机对话记录，因此只有显式传 `-host 0.0.0.0` 才会对外网开放。
- **内存模式**：`-memory` 用内存版 SQLite（`mode=memory&cache=shared`）承载同一套索引，不落盘、不需要 `-db` 目录；代价是没有持久化游标，每次启动都要全量扫描一次。
- **按来源累计用量**：消息数 / Token / 花费以 `(agent, 会话, 来源文件)` 为主键存于 `session_usage`，会话行由各来源求和得出，因此一个横跨多个 rollout 文件的 Codex 续接会话会正确累加而不是相互覆盖。每个来源单独记录「整文件解析 = 覆盖」还是「增量解析 = 累加」，避免手工组合多个绝对/相对标志。
- **活动 / 工具直方图**：`activity`（按天消息数）与 `tool_use`（按工具调用次数）同样以 `(agent, 会话, 来源文件)` 为主键存储。整文件解析视为该来源文件的绝对值（直接替换），增量解析只累加新增行——因为一个 Codex 会话可能横跨多个 rollout 文件，统计必须落到来源文件维度，否则后解析的文件会覆盖先前的统计。来源文件消失且会话再无其他来源时一并清理。
- **正文按需 + 来源提示**：对话内容在点击某个会话时才读取，并使用索引里记录的来源路径直接定位（回退到目录扫描），逐行流式解析，不进常驻内存。
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
internal/index          缓存数据库 + 增量扫描编排（index.go 生命周期/扫描，store.go 写侧，query.go 读侧 API）
internal/api            HTTP API
web                     内嵌前端：原生 ES 模块（无构建），入口 app.js，state/api/util 加 list/dashboard/projects/stats/detail 视图模块
web/smoke.mjs           前端冒烟测试（node，桩 DOM 加载真实模块图，不参与内嵌）
scripts/release-notes.sh 发布说明：从 CHANGELOG.md 抽取当前 tag 的段落，附与上一个稳定 tag 的 compare 链接
```

## API

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/stats?agent=&cwd=&day=` | 会话数 / 消息数 / Token / 花费汇总；`agents` 为按目录统计的各 agent 分布（供标签页），`total` 随 agent + 目录 + 日期筛选 |
| GET | `/api/projects?agent=` | 按工作目录聚合的会话数 / 消息数 / Token / 花费 / 各 agent 分布，按会话数降序 |
| GET | `/api/sessions?agent=&cwd=&q=&day=&sort=&limit=` | 会话概览列表；`day` 只返回该本地日期有活动的会话 |
| GET | `/api/sessions/{agent}/{id}` | 单个会话的完整对话 |
| GET | `/api/activity?agent=&cwd=` | 按本地日期聚合的消息数与会话数（仅 API；看板热力图改用 `/api/trend`） |
| GET | `/api/trend?agent=&cwd=&days=` | 按本地日期的消息 / 会话 / Token / 花费趋势；Token 与花费按会话每日活跃度分摊 |
| GET | `/api/models?agent=&cwd=` | 按模型聚合的会话数 / 消息数 / Token / 花费 |
| GET | `/api/tools?agent=&cwd=&limit=` | 工具调用排行及未截断的总调用次数 |
| GET | `/api/relations?agent=&cwd=` | 处于父子关系中的会话（含父与子），供详情页显示并跳转到主会话 / subagent / 分叉 / 续接的关联会话 |
| POST | `/api/rescan` | 触发增量扫描 |
