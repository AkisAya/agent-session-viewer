# 浏览器原生运行方案（WASM）

把本工具从「本地 Go 进程 + 浏览器访问 localhost」改成「一个纯静态页面，直接在你自己的数据上运行」的方案与实测记录。

本文记录的是**已经动手验证过的结论**，不是设想。文中所有数字都来自本机实测，复现步骤见[附录 A](#附录-a复现步骤)。

## 结论速览

**可行，推荐做，路径是 Go→WASM 而不是用 TS 重写前端。**

| 项目 | 工作量 |
|---|---|
| `internal/source/{claude,codex}` 解析器（约 940 行） | **0 改动** |
| `internal/source/opencode` 解析器（486 行） | 仅换驱动（开库改走 `internal/sqlite`） |
| `internal/index/{index,store,query}.go`（约 1020 行，含 508 行 SQL） | 仅把驱动名与「wasm 一律内存」收进 `internal/sqlite` |
| `internal/api/server.go`（202 行） | **0 改动**（可进程内分发） |
| `internal/model` | **0 改动** |
| `web/*.js` 视图模块（list/dashboard/detail/projects/stats/state/util/md） | **0 改动** |
| `web/api.js` | 改派为本地调用，约 50 行（**未做**） |
| SQLite 驱动 | 换 `ncruces/go-sqlite3`，收在一个新包 `internal/sqlite` 里 |
| 文件层 | **新增**（唯一的新工作，已作为 harness 落地） |
| 入口 | `main.go` 加 build tag，新增 `main_js.go`（**未做**） |

代价：产物从几 MB 变成 **3.9 MB（brotli）**，全量扫描比原生慢约 **5–7 倍**（本机 45 MB 历史实测约 1.1 秒）。

### 实现状态

方案的第一段已经落进仓库，原生路径不受影响（全部用 build tag 隔离）：

| 落地内容 | 位置 |
|---|---|
| 驱动分级 + 「打开数据库」的平台差异 | `internal/sqlite/` |
| OpenCode 的字节挂载（wasm 专用） | 同上，`OpenRead` |
| js/wasm 下索引强制内存 | `internal/index/index.go` |
| 端到端对拍（原生 / wasm 同一份 golden） | `internal/wasmtest/` |
| 手动入口（原生 / Node / 浏览器通用） | `cmd/wasmcheck/` |
| 浏览器 harness（内存文件系统） | `internal/wasmtest/browser/` |

对照[第十节](#十建议的落地顺序)的顺序：第 1、3 步已完成，第 2 步只做了 harness 版（**还没有拖拽 mount，也没有正式入口**），第 4–6 步未开始。因此当前状态是「流水线在浏览器里跑得通且输出与原生逐字节一致」，还不是「可以用的浏览器版」。
`go test` 在原生与 js/wasm 下会比对同一份输出，CI 的 `wasm` 作业跑后者。

---

## 一、为什么不能直接编译

直接编译会失败：

```
$ GOOS=js GOARCH=wasm go build .
modernc.org/libc/errno: build constraints exclude all Go files ...
```

`internal/index` 与 `internal/source/opencode` 依赖 `modernc.org/sqlite v1.60.1`，其底层 `modernc.org/libc v1.77.1` **没有任何 wasm 目标的文件**（只有 darwin/linux/windows/bSD 等平台实现）。

需要注意：这**与「只读还是读写」无关**。只读改变不了编译目标。真正的原因是 modernc 把 C 转译成 Go，转译产物里根本没有 wasm 的平台实现。

解析层本身是干净的——实测这两个包可以独立编到 wasm：

```
$ GOOS=js GOARCH=wasm go build ./internal/source/claude/ ./internal/source/codex/ ./internal/model/
exit=0
```

## 二、驱动替换

`ncruces/go-sqlite3` 把 SQLite 自己编成 wasm（`go-sqlite3-wasm/v6`，通过 `go:embed` 嵌进二进制），再由 wazero 在 Go 里执行。因此它**天生与 GOOS/GOARCH 无关**，可以编到 js/wasm。

实测：仅改 3 个文件（两处 import + 驱动名 `sqlite` → `sqlite3`），

```
$ go test ./...                            # 原生，换驱动后
ok  internal/api   ok  internal/config   ok  internal/index
ok  internal/source  ok  internal/source/claude  ok  internal/source/codex
ok  internal/source/opencode
$ GOOS=js GOARCH=wasm go build .           # 整个项目
exit=0   21.7 MB raw / 5.5 MB gzip / 3.9 MB brotli
$ GOOS=wasip1 GOARCH=wasm go build .
exit=0
```

### 落地方式：一个按平台分级的包

原生没必要承担换驱动的风险，所以驱动选择被收进 `internal/sqlite`，并把两个平台唯一不能共享的操作——**打开一个已经存在的数据库**——也收在同一处：

```go
//go:build !js
package sqlite

import _ "modernc.org/sqlite"

const Driver = "sqlite"
const MemoryOnly = false
func OpenRead(path string) (*sql.DB, error)          // file:…?mode=ro
```

```go
//go:build js
package sqlite

import _ "github.com/ncruces/go-sqlite3/driver"

const Driver = "sqlite3"
const MemoryOnly = true
func OpenRead(path string) (*sql.DB, error)          // os.ReadFile + memdb.Create
```

调用方只写 `sqlite.Driver` 与 `sqlite.OpenRead(path)`，不必知道自己在哪个平台；`MemoryOnly` 用来在 js/wasm 下把索引强制为内存库。两套驱动的 DSN 语法兼容（都支持 `_pragma=`），都走 `database/sql`，切换成本很低。

## 三、js/wasm 下的文件系统

这是方案里最重要的一条约束，也是唯一需要新写代码的地方。

浏览器版 `wasm_exec.js` 提供的 `globalThis.fs` 是一个**全部返回 ENOSYS 的桩**：

```js
globalThis.fs = {
  open(path, flags, mode, callback) { callback(enosys()); },
  stat(path, callback)              { callback(enosys()); },
  readdir(path, callback)           { callback(enosys()); },
  ...
}
```

没有内存文件系统。**Go 的 `os` 包在浏览器里读不了任何文件。**

但有一个关键事实：这个桩只在 `if (!globalThis.fs)` 时安装，因此**可以在加载 `wasm_exec.js` 之前覆写它**。实测这样做是可行的，Go 的 `os.Open` / `os.ReadDir` / `os.Stat` / `filepath.WalkDir` 会正常走我们提供的实现。参见[附录 A](#附录-a复现步骤) 中的 `fs` shim。

Go 侧实际需要的接口面（只读运行时）很小：

| 接口 | 用途 |
|---|---|
| `constants` | `O_WRONLY` / `O_RDWR` / `O_CREAT` / `O_TRUNC` / `O_APPEND` / `O_EXCL` / `O_DIRECTORY` |
| `open` / `close` | `os.Open`、`os.ReadDir`（先 open 再 readdir） |
| `fstat` / `stat` / `lstat` | 大小、修改时间、目录判定 |
| `readdir` | 返回**文件名数组**（Go 期望字符串） |
| `read` | 支持 `position = null`（顺序读）与显式偏移 |
| `write` / `writeSync` | 仅用于 stdout |

`stat` 对象必须提供 Go 会读的字段，且 `mode` 要带文件类型位：

```
dev, ino, mode, nlink, uid, gid, rdev, size, blksize, blocks,
atimeMs, mtimeMs, ctimeMs, isDirectory()
mode: 目录 = 0o040000 | 0o755, 文件 = 0o100000 | 0o644
```

### 两条实现路线

- **A（推荐，实测可用）：覆写 `globalThis.fs`。** Go 代码零改动，shim 全部在 JS 里。需要在页面加载 `wasm_exec.js` **之前**安装。
- **B：在 Go 侧用 `io/fs` 抽象。** 把三个 parser 与 `util.go` 里的 `os.Open` / `os.ReadDir` / `os.Stat` / `filepath.WalkDir` 换成 `fs.FS` 那套，浏览器实现走 `syscall/js`。更 Go 惯用、更好测，但要动约 6 个文件。

既然 A 已经实测跑通整个流程（见下），建议先用 A 落地，把 B 留作后续重构。

## 四、OpenCode 数据源：只能读字节，不能开路径

实测发现一条硬约束。在 js/wasm 下用**文件路径**打开 SQLite 一律失败：

| 打开方式 | 原生 | js/wasm |
|---|---|---|
| 非 WAL 库，`file:...?mode=ro` | ✅ 22 / 1620 | ❌ `sqlite3: disk I/O error` |
| WAL 库，`file:...?mode=ro` | ✅ 22 / 1627 | ❌ `sqlite3: disk I/O error` |
| `.db` 字节 → `memdb` 挂载 | ✅ 22 / 1617 | ✅ **22 / 1617** |

原因不是 WAL，而是 ncruces 的 OS VFS 依赖 mmap，js/wasm 没有。**唯一可行的是「把文件字节挂成数据库」——而这恰好就是浏览器唯一能做的事**（拖进来的文件本来就只有字节）。

`vfs/memdb` 正好覆盖这个用法，并且它会把文件头的 WAL 标志改写为 rollback-journal：

```go
// memdb.Create 内部（来自上游源码）
// Convert data from WAL/2 to rollback journal.
if len(data) >= 20 && (data[18] == 2 && data[19] == 2 || data[18] == 3 && data[19] == 3) {
    data[18] = 1
    data[19] = 1
}
```

所以 WAL 模式的库不给 `-wal` 文件也能读。

**代价**：读到的只是**最后一次 checkpoint 的状态**。实测本机 1627 → 1617 条消息，落后约 **0.6%**。会话数（`session_v2` 22 行）没有差异。这个误差需要在 UI 上如实说明，或者提示用户「同时拖入 `-wal` 文件」——后者需要 WAL/shm 支持，当前不可行。

对应的实现就落在 `internal/sqlite.OpenRead` 里（wasm 专用分支，原生不受影响）：

```go
//go:build js

func OpenRead(path string) (*sql.DB, error) {
    raw, err := os.ReadFile(path)
    if err != nil {
        return nil, err
    }
    // memdb.Create takes ownership of the slice and rewrites the header in place.
    name := memdbName(path)          // 见下方注意
    memdb.Create(name, raw)
    return sql.Open(Driver, "file:/"+name+"?vfs=memdb")
}
```

`opencode.Source.open()` 因此收缩成一行 `return sqlite.OpenRead(s.Path)`，它不需要知道自己在哪个平台。

> 注意 `memdb.Create` 的名字**不带**前导斜杠，DSN 才带（`Create("a.db")` 对应 `file:/a.db?vfs=memdb`）。写错会得到 `unable to open database file`。实现里用「文件名 + 路径哈希」而不是直接拿路径当名字：名字会进 DSN，不能带 `?` `&` 这类标点。

## 五、索引与 SQLite 的其它用途

- **索引缓存**：浏览器版用内存库（等价于现有的 `-memory` 模式）。没有持久化，每次打开重扫一遍——本机实测约 1.1 秒，可以接受。
- **索引缓存的平台差异**：js/wasm 下「内存库」不是选择而是硬约束——没有 mmap，文件 VFS 打不开任何东西——因此 `internal/sqlite.MemoryOnly` 为真时 `index.Open` 直接忽略 `Path`。构造时用的仍是 `file:asv-<pid>?mode=memory&cache=shared`；浏览器里 `os.Getpid()` 返回 `-1`，实测能工作。
- **将来想要「二次秒开」**：`ext/serdes` 提供 `Serialize` / `Deserialize`，可以把整个内存索引库快照成 `[]byte` 存进 IndexedDB，下次直接还原。**不需要 OPFS。**

## 六、HTTP 层与入口

浏览器里不能 `ListenAndServe`，但 `internal/api` 的 mux 可以进程内驱动——实测 `net/http` + `net/http/httptest` 能编到 js/wasm：

```go
rec := httptest.NewRecorder()
mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/sessions", nil))
```

因此 `server.go`、`query.go` 一行都不用改，只需要：

1. `main.go` 加 `//go:build !js`，保持现有行为；
2. 新增 `main_js.go`（`//go:build js`），用 `syscall/js` 暴露一个函数，例如 `api(method, path) -> {status, body}`；
3. `web/api.js` 里把 `fetch` 换成该函数的调用——**这是前端唯一需要改的文件**。

## 七、入口交互（mount）

浏览器没有 `~/.claude` 这种路径，文件必须由用户交出来。

- **拖拽应当是主路径。** macOS 的目录选择器默认不显示点开头的目录（`~/.claude` 需要先按 `Cmd+Shift+.`）。拖文件夹在全部浏览器都可用，`webkitGetAsEntry()` 可以递归；Chromium 还能用 `getAsFileSystemHandle()` 拿到可持久化的 handle。
- **按结构嗅探自动归类**：含 `projects/` → Claude 根；含 `sessions/` → Codex 根；`*.db` → OpenCode。用户只需把 `~/.claude`、`~/.codex`、`opencode.db` 一起拖进来。
- **持久授权**：Chromium 可以把 handle 存进 IndexedDB 并 `queryPermission` / `requestPermission`。Firefox/Safari 不支持，只能每次重拖。

### 扫描应当放进 Web Worker

1.1 秒的同步阻塞在界面上是可感知的。解析器是纯函数（不碰 DOM），可以整体搬进 Worker；`DirectoryHandle` 是可结构化克隆的。需要回传进度（已扫描 n/N）。

---

## 八、实测记录

环境：Go 1.26.8 (darwin/arm64)、`ncruces/go-sqlite3 v0.35.6`、Node v24.21.0、Google Chrome（headless）。

### 8.1 正确性对拍（同一份 wasm，静态 fixture）

原生与浏览器各跑一遍同一批 fixture（2 个 Claude 会话、1 个 Codex rollout、1 个 OpenCode 库），**输出逐字节一致**：

```
sessions=4 messages=9 tokens_in=30 tokens_out=40 cache_read=5 cost=0.2500
  agent=claude    sessions=2      messages=5
  agent=codex     sessions=1      messages=2
  agent=opencode  sessions=1      messages=2
transcript(claude): 3 msgs   transcript(opencode): 4 msgs   transcript(codex): 2 msgs
```

### 8.2 真实数据规模

| 数据源 | 文件数 | 大小 |
|---|---|---|
| `~/.claude/projects` | 5 个 `.jsonl` | 1.6 MB |
| `~/.codex/sessions` | 31 个 `.jsonl` | 12 MB |
| `~/.local/share/opencode/opencode.db` | 1 | 31.1 MB |

扫描结果：**56 个会话 / 约 2000 条消息**（Claude 5、Codex 29、OpenCode 22）。

### 8.3 性能：原生 vs js/wasm（Node，真实数据）

| 阶段 | 原生 | js/wasm | 倍率 |
|---|---|---|---|
| 打开索引 | 1–5 ms | 123–127 ms | 一次性启动 |
| **全量扫描** | 171–191 ms | **1301 ms** | **约 7×** |
| 列表 / 看板查询 | < 1 ms | 1–7 ms | 可忽略 |
| 读单个会话正文 | 2–3 ms | 13–19 ms | 约 6× |

### 8.4 性能：真实浏览器（Chrome headless，真实数据）

headless 的 `--virtual-time-budget` 会快进时钟，页面自己测的时间不可用，因此改用**服务端墙钟**（虚拟时间只快进定时器，不快进真实 CPU 计算）：

```
 9811.0 ms  /app.wasm    ← 15 MB 模块下载完
10895.3 ms  /__perf      ← 页面全部跑完
────────────────────────
  1084 ms  = wasm 实例化 + 全量扫描 + 全部看板查询 + 3 个会话正文
```

浏览器里扫描结果同样正确（56 个会话、三个 agent 都齐全）。消息数与 Node 运行略有出入（2015 / 2031 等），因为 `opencode.db` 是活的、正在被写入，不是误差。

### 8.5 体积

| 构建 | raw | gzip | brotli |
|---|---|---|---|
| 完整应用（含 `net/http`） | 21.7 MB | 5.5 MB | **3.9 MB** |
| 仅解析 + 索引（不含 `net/http`） | 15.7 MB | — | — |

---

## 九、风险与尚未验证的部分

1. **懒加载读取未验证。** 实测的 shim 是「一次性把所有字节读进内存」。真实应用更可能想按需读（`FileSystemAccess` 的 `handle.getFile()` 是异步的）。Go 的 `fs_js` 支持异步回调（Node 的 `fs` 本来就是异步的），理论上成立，但**没有实测**。
2. **内存峰值未测量。** 45 MB 字节 + `memdb` 副本 + Go 堆，未触顶但未量化。真实应用应当在 mount 时按需读取而不是全量预载。
3. **只测了 Chrome。** Firefox/Safari 的 wasm 执行速度另说，`FileSystemAccess` 本来也只有 Chromium 系有。
4. **OpenCode 快照落后约 0.6%**（见第四节）。需要在 UI 上说明。
5. **体积增加约 5 倍**（相对纯前端方案）。对一个本地工具可以接受，但不是同一量级。
6. **`wasm-in-wasm` 的性能没有进一步优化空间已被验证。** 当前 1.1 秒可接受；若将来历史规模大幅增长，需要重新评估（可选方向：索引持久化避免重扫，而不是优化扫描本身）。

## 十、建议的落地顺序

1. **驱动替换**（build tag 分开原生/wasm）+ `main_js.go` 入口。此时应用还跑不起来，但能验证编译链路。
2. **`globalThis.fs` shim**（路线 A）+ 拖拽 mount UI（先做结构嗅探与内存文件表）。这一步之后应当能看到真实数据渲染出来。
3. **OpenCode 的 memdb 钩子**。
4. **`api.js` 改派**。
5. **Worker 化 + 进度回传**。
6. （可选）**IndexedDB 快照**做二次秒开。

第 2 步做完就能判断整条路是否成立——若数据能渲染出来，后面基本是收尾。

**当前进度**：1、3、以及 2 的 shim 部分已完成并入库（见[实现状态](#实现状态)），且有用例守住「原生 == wasm」。剩下的关键一步是 2 的 **mount UI**——把用户拖进来的文件表接到 shim 上；这一步做完就能在浏览器里看到真实数据。

## 附录 A：复现步骤

### 1. 驱动链路

仓库里已经落地（见[实现状态](#实现状态)），直接跑即可：

```bash
go test ./...                                   # 原生，行为与换驱动前一致
GOOS=js GOARCH=wasm go build -o /tmp/app.wasm .  # 整个项目编到 wasm
```

早期是在仓库副本里手工做的（`sed` 替换两处 import 与驱动名 `sqlite` → `sqlite3`，
再 `go get github.com/ncruces/go-sqlite3`），结论与现在入库的实现一致。

### 2. Node 下跑真实数据

`cmd/wasmcheck` 复用真实的 `internal/index` + `internal/source`，把真实 home 指给它：

```bash
go run ./cmd/wasmcheck -home "$HOME"                                    # 原生
GOOS=js GOARCH=wasm go build -o /tmp/wasmcheck.wasm ./cmd/wasmcheck
node "$(go env GOROOT)/lib/wasm/wasm_exec_node.js" /tmp/wasmcheck.wasm -home "$HOME"
```

Node 的 `wasm_exec_node.js` 使用**真实的 `fs`**，所以 Go 的 `os` 包在 Node 下完全可用——这一步专门用来测运行时正确性与性能，不需要 shim。

### 3. 浏览器

```bash
./internal/wasmtest/browser/build.sh
python3 -m http.server -d internal/wasmtest/browser/dist 8000
```

页面（`internal/wasmtest/browser/`）把 `vfs.js` 装到 `globalThis.fs` 上再加载
`wasm_exec.js`。顺序是硬要求：**先装 fs，再加载 `wasm_exec.js`，最后实例化**——
`wasm_exec.js` 只在 `!globalThis.fs` 时才装它自己那个全是 `ENOSYS` 的空桩。

这份 shim（`vfs.js`）里有几个不显然的点，都是实测踩出来的：

- `constants` 必须是 Node 的真实取值（`O_WRONLY`=1 / `O_RDWR`=2 / `O_CREAT`=64 / `O_EXCL`=128 / `O_TRUNC`=512 / `O_APPEND`=1024 / `O_DIRECTORY`=65536）。Go 用它们把自己的 `openmode` 翻译成 Node 的 flags 位。
- 错误对象的 `code` 必须是 `$GOROOT/src/syscall/tables_js.go` 里 `errnoByCode` 认识的值；不认识的 code 会让 Go 直接 **panic**（是 panic，不是返回错误）。
- `readdir` 返回**文件名字符串数组**，不是 dirent 对象。
- `stat` 必须给出 `mode` 的类型位（目录 `0o040000|0o755`，文件 `0o100000|0o644`）；Go 靠它判断是不是目录。
- `globalThis.fs` 不是唯一要补的全局：`wasm_exec.js` 同时提供 `globalThis.path` 与 `globalThis.process`（`pid` 为 `-1`）。Go 的 `syscall` **每次 open 都会调 `path.resolve`**（结果会作为文件名存下来），`os.Getwd` 会调 `process.cwd()`。
- 写文件也走同一套（`O_CREAT` / `O_TRUNC` / `ftruncate`），所以 shim 必须是可写的；harness 正是靠这一点让 Go 自己去 `MkdirTemp` + 写固件。

### 4. 测浏览器墙钟

`--dump-dom --virtual-time-budget` 会**快进 `performance.now()`**（实测页面上显示 59990 ms），所以它只能用来验收输出正确性。测真实时间要让页面把结果回传服务器，用**服务端**时间戳计算。

