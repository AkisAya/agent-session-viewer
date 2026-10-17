# Agent Session Viewer

一个轻量的本地工具，用于浏览本机上 **Claude Code**、**Codex** 和 **OpenCode** 的历史对话记录。

- 单个 Go 二进制，内嵌前端页面
- 增量索引：只扫描新增内容，二次启动几乎瞬时
- 空闲低占用，不主动轮询，启动时扫描或手动刷新

<img src="ui_example.webp" alt="界面示例" width="560">

## 快速开始

```bash
go build -o asv .
./asv            # 默认：索引持久化到 SQLite 缓存
./asv -memory    # 可选：索引只放内存，不创建/读写缓存数据库
```

然后打开 http://localhost:8787 。

前端功能：

- 视图切换：**会话**（列表）/ **看板**（热力图与统计面板），作用于当前的 agent 标签与项目筛选
- 顶部标签切换：全部 / Claude / Codex / OpenCode，各标签带会话数量
- 主题切换：跟随系统 / 浅色 / 深色（记住选择，无刷新闪烁）
- 左侧项目目录侧边栏：按工作目录查看会话，标题显示目录总数，各目录显示会话数，点击筛选（窄屏自动切换为下拉框）
- 会话列表只展示概览卡片（不额外堆叠目录统计面板）
- 概览卡片：按 agent 配色的左侧色条、标题、首条用户消息摘要、消息数、Token、花费、更新时间；主会话卡片带 `subagent ×N` 标记，**subagent 会话不在列表中单独出现**，从主会话详情页的关联链接打开（详情页内点 subagent 标记可跳回主会话）
- 每张卡片带 **⧉ resume 按钮**：点击把该会话的恢复命令（`claude --resume <id>` / `codex resume <id>` / `opencode --session <id>`）复制到剪贴板，回终端粘贴即可继续
- 搜索（标题 / 目录 / 内容摘要，匹配项高亮）、排序（更新时间 / 创建时间 / 消息数 / Token）；过长的目录路径按头尾保留、中间省略的方式截断（完整路径悬浮可见）
- 点击卡片查看完整对话：展示标题、目录、模型、统计信息（对话 / 思考 / 工具分开计数），可折叠「思考 / 工具」内容
- **对话 Markdown 渲染**：标题 / 列表 / 表格 / 引用 / 代码块（带语言标签与复制按钮）/ 链接与图片（仅放行 http(s)、mailto，其余按纯文本转义）
- **消息骨架导航**：右侧大纲栏按顺序列出可见消息，点击跳转，阅读位置变化时自动高亮当前消息
- **使用热力图**（看板）：按本地日期统计每日消息数，GitHub 风格色阶 + 悬浮提示，附活跃天数 / 最长连续天数统计；可切换指标（消息 / 会话 / Token / 花费）、时间跨度（90 / 180 / 365 天），带色阶图例；**点击某天可把列表筛选到当天**（列表上方显示日期小标签，可一键清除）
- **看板统计**：在热力图之外提供趋势（消息 / Token / 花费，按会话每日活跃度近似分摊）、模型分布、项目排行、工具排行（跨 agent 归一化，`Bash` / `exec_command` / `shell` 统一为 `shell`，显示次数与涉及会话数）与 Agent 对比，均随当前 agent 标签与项目筛选联动
- **关联跳转**：subagent 会话不在列表中单独列出，从主会话详情页的关联链接打开；详情页内点 subagent 标记可跳回主会话，分叉（OpenCode `fork_session_id`）/ 续接（Codex `parent_thread_id`）也会在详情页显示父子关系链接
- **顶栏统计随筛选联动**：会话数 / Token / 花费跟随当前 agent、项目与日期筛选变化，Token 拆分为输入 `↑` / 输出 `↓` / 缓存读 `⚡`，并显示「更新于 X 分钟前」
- **URL 状态**：agent / 项目 / 视图 / 排序 / 搜索 / 日期写入查询参数，可分享或收藏某个筛选视图，浏览器前进后退可用
- 详情页消息带**时间戳并按天分隔**；「导出」按钮把整段对话复制为 Markdown
- 卡片显示**模型**；卡片 / 项目行可键盘聚焦，Enter / Space 激活；尊重系统「减少动态效果」偏好；超长对话采用按需渲染
- 「刷新」按钮触发一次增量扫描

## 数据来源

| Agent | 路径 | 说明 |
|---|---|---|
| Claude Code | `~/.claude/projects/<编码路径>/<sessionId>.jsonl` | 标题取 `custom-title` / `ai-title` / `last-prompt`，费用取 `cost-state`，思考取 `thinking` 块，工具调用取 `tool_use` 块，活动按行内 `timestamp` 归日 |
| Codex | `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl` | 标题优先取 `session_index.jsonl`，模型取 `turn_context`，Token 取累计 `token_count`，回退解析 `response_item`；工具调用优先取 `event_msg` 的 `CommandExecution`/`WebSearch`/`McpToolCall`，否则取 `response_item` 的 `function_call`/`custom_tool_call`；`parent_thread_id` 非自身时记为续接 |
| OpenCode | `~/.local/share/opencode/opencode.db` | 只读打开，读取 `session_v2` 与 `session_message`（兼容旧的 `message`/`part`）；活动按 `time_created` 归日，工具调用取 assistant 消息中的 `tool` 内容块；`parent_id` 记为子代理（`subagent`），`fork_session_id` 记为分叉（`fork`） |

## 命令行参数

```
-port int              HTTP 端口（默认 8787）
-db string             缓存数据库路径（默认 ~/.local/share/agent-session-viewer/index.db，-memory 时忽略）
-claude-dir string     Claude projects 目录
-codex-dir string      Codex sessions 目录
-codex-index string    Codex session_index.jsonl 路径
-opencode-db string    OpenCode 数据库路径
-watch-interval int    每隔 N 秒自动增量扫描（0 = 仅按需，默认）
-memory                索引只保存在内存中，不创建也不读写缓存数据库（每次启动全量扫描）
```

## 设计要点

- **增量索引**：缓存数据库（SQLite）为每个源文件记录 `size` / `mtime` / 解析偏移量。文件未变化时整文件跳过；有追加时只解析新增字节并累加统计。
- **按需加载正文**：列表只读缓存，对话内容在点击某个会话时才从源文件读取，不进常驻内存。
- **稳定 ID**：解析出的 session id 随游标一起持久化，保证增量扫描时不会因为读不到文件头而错配。
- **只读访问**：读取 OpenCode 数据库与磁盘文件均为只读，不影响正在运行的 agent。
- **内存模式**：`-memory` 用内存版 SQLite（`mode=memory&cache=shared`）承载同一套索引，不落盘、不需要 `-db` 目录；代价是没有持久化游标，每次启动都要全量扫描一次。
- **活动 / 工具直方图**：`activity`（按天消息数）与 `tool_use`（按工具调用次数）以 `(agent, 会话, 来源文件)` 为主键存储。整文件解析视为该来源文件的绝对值（直接替换），增量解析只累加新增行——因为一个 Codex 会话可能横跨多个 rollout 文件，统计必须落到来源文件维度，否则后解析的文件会覆盖先前的统计。来源文件消失或会话被清理时同步删除直方图。
- **工具名归一化**：三个 agent 的工具命名各不相同（Claude 的 `Bash`、Codex 的 `exec_command`、OpenCode 的 `shell`/`bash`），在解析阶段统一映射到规范名，MCP 工具保留为 `mcp:server.tool`。

## 项目结构

```
main.go                 CLI 入口
internal/config         路径与参数解析
internal/model          归一化的 Session / Message / Stats
internal/source         数据源接口与增量 Delta 定义
internal/source/claude  Claude Code 解析
internal/source/codex   Codex 解析（跨 rollout 文件合并对话）
internal/source/opencode OpenCode SQLite 解析
internal/index          缓存数据库 + 增量扫描编排
internal/api            HTTP API
web                     内嵌前端（HTML/CSS/JS）
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
