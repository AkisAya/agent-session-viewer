# 浏览器 harness

在真实浏览器里跑一遍完整流水线（扫描 → 索引 → 查询），并把输出与 Go 测试用
的同一份 `testdata/golden.txt` 逐字节比对。

```bash
./build.sh
python3 -m http.server -d dist 8000
open http://localhost:8000/
```

页面会把结论显示在最上方：`PASS` 表示浏览器里的输出与原生一致。

## 三个文件

- `vfs.js`——给 Go 用的内存文件系统。Go 的 js/wasm 支持通过 `globalThis.fs`
  上的 Node `fs` API 访问宿主，浏览器没有这个东西，所以这里用 `Map` 实现一份。
  它**必须在 `wasm_exec.js` 之前加载**：`wasm_exec.js` 只在 `globalThis.fs`
  缺失时才装自己那个全是 `ENOSYS` 的空桩。同时它也提供 `globalThis.path` 与
  `globalThis.process`（`cwd()` 返回 `/`，`pid` 为 `-1`）。
- `index.html`——装 wasm、跑 `cmd/wasmcheck`、抓 stdout 与 golden 对比。
- `build.sh`——把 `app.wasm`、`wasm_exec.js`、`golden.txt` 拷进 `dist/`。

## 为什么固件是 Go 写的

页面不预置任何数据：`wasmcheck` 在 `-home` 为空时自己 `MkdirTemp` +
写固件树，而这正落在 `vfs.js` 上。这样一来 JS 侧只有「空的可写文件系统」，
固件（三个来源的目录结构、`opencode.db` 的字节）仍然只有 Go 那一份定义。

## 注意

- 必须走 HTTP：页面要 `fetch` `app.wasm` 与 `golden.txt`，`file://` 会被拦。
- Chrome 无头模式的 `--virtual-time-budget` 会快进 `performance.now()`，页面
  上显示的耗时不是真实墙钟时间；要测性能请用 `cmd/wasmcheck` 配合服务端计时。
- `dist/` 是组装产物，不入库（见 `.gitignore`）。
