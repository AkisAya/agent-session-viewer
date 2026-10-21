<div align="center">

<img src="web/logo.svg" width="104" alt="Agent Session Viewer">

# Agent Session Viewer

### 所有 agent 的对话，一处回看。

**Claude Code · Codex · OpenCode · DeepSeek Harness**<br>
一个 Go 二进制，内嵌前端，索引增量更新。<br>
会话历史、对话全文与用量统计随手可查，数据始终留在本机。

[![Release](https://img.shields.io/github/v/release/AkisAya/agent-session-viewer?label=release&color=111111)](https://github.com/AkisAya/agent-session-viewer/releases/latest)
[![Stars](https://img.shields.io/github/stars/AkisAya/agent-session-viewer?style=flat&color=111111)](https://github.com/AkisAya/agent-session-viewer/stargazers)
[![CI](https://img.shields.io/github/actions/workflow/status/AkisAya/agent-session-viewer/ci.yml?label=CI&color=111111)](https://github.com/AkisAya/agent-session-viewer/actions/workflows/ci.yml)
[![Go](https://img.shields.io/github/go-mod/go-version/AkisAya/agent-session-viewer?color=111111)](go.mod)<br>
![macOS](https://img.shields.io/badge/macOS-000000?logo=apple&logoColor=white)
![Linux](https://img.shields.io/badge/Linux-FCC624?logo=linux&logoColor=black)
![Windows](https://img.shields.io/badge/Windows-0078D4?logo=windows&logoColor=white)

<br>

<a href="docs/screenshots/list.webp"><img src="docs/screenshots/list.webp" width="680" alt="会话列表：分页、卡片/表格视图，按 agent 与项目筛选"></a>

<sub>点击查看原图 · 截图使用合成的演示数据，可用 <code>node scripts/screenshots.mjs</code> 重新生成</sub>

</div>

<br>

## 它解决什么

每个 agent 都把历史存在自己的目录、自己的格式里：Claude Code 按项目分目录写 JSONL，Codex 按日期滚存，OpenCode 存进 SQLite，DSH 是 zstd 帧拼成的追加日志。想回头找一句回复、算一下这个月花了多少，只能在四个地方各翻一遍。

本工具把这些记录索引到一个统一的只读视图里：

- **概览**：会话列表按 agent / 项目筛选，分页、卡片 / 表格两种密度、收藏与本地标签。
- **详情**：对话详情支持 Markdown 渲染、查找、跳转用户消息、消息大纲、宽屏 / 字号；子代理、分叉与续接关系可互相跳转。
- **看板**：KPI、活动热力图、趋势、Top 会话、工作节奏、模型 / 工具排行与可排序对比表；Codex / DSH 等没有记录花费的来源按参考单价显示「≈」估算。
- **轻量**：单个 Go 二进制，前端内嵌；增量索引只扫新增内容，二次启动几乎瞬时，空闲低占用。顶栏「自动刷新」或 `-watch-interval` 可让索引 / 页面保持更新（通常二选一）。

所有源文件都以只读方式打开，不影响正在运行的 agent；HTTP 默认只绑定 `127.0.0.1`。它是一个本地工具，不是云端服务——没有账号，没有同步。

## 截图

| 统计看板 | 对话详情 | 会话列表 |
| :--: | :--: | :--: |
| <a href="docs/screenshots/dashboard.webp"><img src="docs/screenshots/dashboard-thumb.webp" width="100%" alt="统计看板"></a> | <a href="docs/screenshots/detail.webp"><img src="docs/screenshots/detail.webp" width="100%" alt="对话详情"></a> | <a href="docs/screenshots/list.webp"><img src="docs/screenshots/list.webp" width="100%" alt="会话列表"></a> |

**统计看板**：概览卡片、活动热力图与趋势、工作节奏（近 7 天 / 今天按小时，可切换消息 / 会话 / Token / 花费）、Top 会话、模型 / 工具排行、Agent 对比与项目排行。

**对话详情**：Markdown 渲染、查找与用户消息跳转、右侧消息大纲、上下文压缩标记、深链与 Markdown / JSON 导出。

## 快速开始

### 安装

**脚本安装**（推荐；macOS arm64 / amd64、Linux amd64 / arm64、Windows amd64，Windows 在 Git Bash 中运行）：

```bash
curl -fsSL https://raw.githubusercontent.com/AkisAya/agent-session-viewer/main/scripts/install.sh | bash
```

自动识别平台，下载最新 release 并安装 `asv` 到 `/usr/local/bin`（不可写时装到 `~/.local/bin` 并提示加入 `PATH`）。`VERSION=v0.5.0` 指定版本，`ASV_INSTALL_DIR=/path` 改安装目录。也可从 [Releases](https://github.com/AkisAya/agent-session-viewer/releases) 手动下载。

**源码构建**（需要 Go 1.26+）：

```bash
git clone https://github.com/AkisAya/agent-session-viewer.git
cd agent-session-viewer
go build -o asv .
```

### 运行

```bash
asv            # 默认：索引持久化到 SQLite 缓存，二次启动增量扫描
asv -open      # 启动后自动打开浏览器
asv -memory    # 可选：索引只放内存，不创建/读写缓存数据库
asv -rebuild   # 删除缓存并全量重建（索引口径变更或缓存损坏时用）
```

运行后打开 http://localhost:8787

服务会先启动、再在后台扫描，首次建立索引期间页面会显示扫描进度；之后只扫描新增内容，二次启动几乎瞬时。

## 数据来源

| Agent | 路径 |
|---|---|
| Claude Code | `~/.claude/projects/<编码路径>/<sessionId>.jsonl` |
| Codex | `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl` |
| OpenCode | `~/.local/share/opencode/opencode.db`（只读打开） |
| DSH | `~/.dsh/sessions/<编码 cwd>/<sessionId>/session.vN.jsonl.zstd`（未压缩的 `session.vN.jsonl` 同样支持） |

会话标题、模型、Token、花费、工具调用与每日活动都从上述文件解析；子代理、分叉和续接关系也会自动识别并互相跳转。解析细节见 [设计说明](docs/DESIGN.md)。

四个 agent 的历史路径在各平台一致：
- Windows 上 `~` 即 `%USERPROFILE%`
- OpenCode 也沿用 Unix 风格的 `~/.local/share/opencode`
- DSH 默认读取 `~/.dsh/sessions`，设置了 `DSH_HOME` 时改为 `$DSH_HOME/sessions`
- 本工具自己的索引数据库遵循平台习惯——Windows 下默认 `%LOCALAPPDATA%\agent-session-viewer\index.db`，其他平台 `~/.local/share/agent-session-viewer/index.db`（`-db` 可覆盖）

收藏、标签、主题、卡片/表格偏好等只保存在当前浏览器（localStorage），不会上传或同步。花费优先使用来源记录的真实值；Codex / DSH 等没有花费字段时，按 `web/pricing.js` 里的参考单价估算并显示 `≈`，仅供参考。从源码构建时可按需修改该表。

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
-rebuild               启动前删除缓存数据库，强制全量重建
-open                  监听成功后用默认浏览器打开页面
-version               打印版本号后退出
```

顶栏「自动刷新」是浏览器侧定时任务（默认关闭）：每 30 秒让后端增量扫描日志、更新索引，再重新加载统计/列表/看板；不是整页 reload，也不会检查软件版本。`-watch-interval` 是服务端定时扫描，页面会通过 `/api/scan` 自动跟随。**两者通常二选一**：页面常开时用顶栏即可；需要在浏览器关闭后仍保持索引新鲜（或多标签、外部 API 场景）时用 `-watch-interval`。同时开启不会重复写索引（有并发保护），但客户端定时请求是多余的。

## 文档

| | |
| :-- | :-- |
| [docs/DESIGN.md](docs/DESIGN.md) | 解析细节、索引设计、项目结构与 HTTP API |
| [CHANGELOG.md](CHANGELOG.md) | 版本变更记录 |

遇到问题或想提建议，欢迎开 [issue](https://github.com/AkisAya/agent-session-viewer/issues)。如果这个工具帮你省下了一次翻目录的时间，点个 ⭐ 能让更多人找到它。
