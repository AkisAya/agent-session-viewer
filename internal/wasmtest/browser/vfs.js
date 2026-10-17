// vfs.js -- an in-memory filesystem for Go's js/wasm runtime.
//
// Go's js/wasm support reaches the host through the Node "fs" API on
// globalThis. A browser has no such thing, so this file provides one: a small
// filesystem that lives entirely in a Map. It must be installed *before*
// wasm_exec.js is loaded -- wasm_exec.js only defines its own (entirely
// non-functional) stub when globalThis.fs is absent.
//
// Nothing in the Go code knows this exists. internal/source reads files by path
// via os.ReadFile, os.MkdirAll and friends, and internal/sqlite mounts the
// bytes it gets back into SQLite's in-memory VFS. Overwriting globalThis.fs is
// therefore all that is needed to run the real pipeline in a browser.
//
// The API implemented here is the subset of Node's fs that Go actually uses;
// see $(go env GOROOT)/src/syscall/fs_js.go for the authoritative list, and
// $(go env GOROOT)/lib/wasm/wasm_exec.js for the stub this replaces.

"use strict";

(() => {
  // Node's fs constants, which is what syscall/fs_js.go passes through.
  const S_IFDIR = 0o040000;
  const S_IFREG = 0o100000;
  const O_WRONLY = 1;
  const O_RDWR = 2;
  const O_CREAT = 64;
  const O_EXCL = 128;
  const O_TRUNC = 512;
  const O_APPEND = 1024;
  const O_DIRECTORY = 65536;

  const decoder = new TextDecoder("utf-8");
  // stdout/stderr are decoded as a stream: a multi-byte rune can be split
  // across two writes.
  const stdoutDecoder = new TextDecoder("utf-8");
  const stderrDecoder = new TextDecoder("utf-8");

  /** absolute path -> { dir, data, mtimeMs } */
  const nodes = new Map();
  /** fd -> { path, flags, pos } */
  const handles = new Map();
  let nextFd = 3;

  let stdout = "";
  let stderr = "";

  // Go maps a JS error to an errno through error.code, and panics outright on a
  // code it does not know. Only codes from syscall/errnoByCode may be used.
  function fail(code, syscall, path) {
    const err = new Error(`${code}: ${syscall}, '${path}'`);
    err.code = code;
    err.errno = -1;
    err.syscall = syscall;
    err.path = path;
    return err;
  }

  function reply(cb, err, value) {
    if (err) cb(err, undefined);
    else cb(null, value);
  }

  function normalize(p) {
    const out = [];
    for (const seg of String(p).split("/")) {
      if (seg === "" || seg === ".") continue;
      if (seg === "..") {
        out.pop();
        continue;
      }
      out.push(seg);
    }
    return "/" + out.join("/");
  }

  function parentOf(p) {
    const i = p.lastIndexOf("/");
    return i === 0 ? "/" : p.slice(0, i);
  }

  function lookup(p, syscall) {
    const path = normalize(p);
    const node = nodes.get(path);
    if (!node) throw fail("ENOENT", syscall, path);
    return { path, node };
  }

  // The stat shape syscall/setStat reads. mode must carry the file-type bits:
  // that is how Go decides whether it is looking at a directory.
  function statOf(node) {
    const size = node.dir ? 0 : node.data.length;
    return {
      dev: 1,
      ino: 1,
      mode: node.dir ? S_IFDIR | 0o755 : S_IFREG | 0o644,
      nlink: node.dir ? 2 : 1,
      uid: 0,
      gid: 0,
      rdev: 0,
      size,
      blksize: 4096,
      blocks: Math.ceil(size / 512),
      atimeMs: node.mtimeMs,
      mtimeMs: node.mtimeMs,
      ctimeMs: node.mtimeMs,
      isDirectory: () => node.dir,
    };
  }

  // A stand-in for stdin/stdout/stderr, which Go may fstat.
  function fdStat() {
    return {
      dev: 1, ino: 1, mode: S_IFREG | 0o644, nlink: 1, uid: 0, gid: 0, rdev: 0,
      size: 0, blksize: 4096, blocks: 0,
      atimeMs: 0, mtimeMs: 0, ctimeMs: 0,
      isDirectory: () => false,
    };
  }

  function mkdirOne(path, syscall) {
    if (path === "/") throw fail("EEXIST", syscall, path);
    const parent = nodes.get(parentOf(path));
    if (!parent) throw fail("ENOENT", syscall, path);
    if (!parent.dir) throw fail("ENOTDIR", syscall, parentOf(path));
    if (nodes.has(path)) throw fail("EEXIST", syscall, path);
    nodes.set(path, { dir: true, data: null, mtimeMs: Date.now() });
  }

  function mkdirAll(path) {
    if (path === "/") return;
    const parent = parentOf(path);
    if (!nodes.has(parent)) mkdirAll(parent);
    if (!nodes.has(path)) nodes.set(path, { dir: true, data: null, mtimeMs: Date.now() });
  }

  function writeAt(node, data, at) {
    const end = at + data.length;
    if (end > node.data.length) {
      const grown = new Uint8Array(end);
      grown.set(node.data);
      node.data = grown;
    }
    node.data.set(data, at);
    node.mtimeMs = Date.now();
  }

  function resize(node, size) {
    const next = new Uint8Array(size);
    next.set(node.data.subarray(0, Math.min(size, node.data.length)));
    node.data = next;
    node.mtimeMs = Date.now();
  }

  globalThis.fs = {
    constants: { O_WRONLY, O_RDWR, O_CREAT, O_TRUNC, O_APPEND, O_EXCL, O_DIRECTORY },

    open(path, flags, mode, cb) {
      try {
        const abs = normalize(path);
        let node = nodes.get(abs);
        if (!node) {
          if (!(flags & O_CREAT)) throw fail("ENOENT", "open", abs);
          const parent = nodes.get(parentOf(abs));
          if (!parent) throw fail("ENOENT", "open", abs);
          if (!parent.dir) throw fail("ENOTDIR", "open", abs);
          node = { dir: false, data: new Uint8Array(0), mtimeMs: Date.now() };
          nodes.set(abs, node);
        } else if (flags & O_CREAT && flags & O_EXCL) {
          throw fail("EEXIST", "open", abs);
        }
        if (flags & O_DIRECTORY && !node.dir) throw fail("ENOTDIR", "open", abs);
        if (node.dir && flags & (O_WRONLY | O_RDWR | O_TRUNC)) throw fail("EISDIR", "open", abs);
        if (!node.dir && flags & O_TRUNC) resize(node, 0);
        const fd = nextFd++;
        handles.set(fd, { path: abs, flags, pos: 0 });
        reply(cb, null, fd);
      } catch (err) {
        reply(cb, err);
      }
    },

    close(fd, cb) {
      try {
        if (fd > 2 && !handles.delete(fd)) throw fail("EBADF", "close", String(fd));
        reply(cb, null);
      } catch (err) {
        reply(cb, err);
      }
    },

    fstat(fd, cb) {
      try {
        if (fd <= 2) return reply(cb, null, fdStat());
        const handle = handles.get(fd);
        if (!handle) throw fail("EBADF", "fstat", String(fd));
        reply(cb, null, statOf(nodes.get(handle.path)));
      } catch (err) {
        reply(cb, err);
      }
    },

    stat(path, cb) {
      try {
        reply(cb, null, statOf(lookup(path, "stat").node));
      } catch (err) {
        reply(cb, err);
      }
    },

    lstat(path, cb) {
      try {
        reply(cb, null, statOf(lookup(path, "lstat").node));
      } catch (err) {
        reply(cb, err);
      }
    },

    // Go treats the result as a plain array of names, not as dirent objects.
    readdir(path, cb) {
      try {
        const { path: abs, node } = lookup(path, "readdir");
        if (!node.dir) throw fail("ENOTDIR", "readdir", abs);
        const prefix = abs === "/" ? "/" : abs + "/";
        const names = [];
        for (const key of nodes.keys()) {
          if (key === abs || !key.startsWith(prefix)) continue;
          const rest = key.slice(prefix.length);
          if (!rest.includes("/")) names.push(rest);
        }
        reply(cb, null, names);
      } catch (err) {
        reply(cb, err);
      }
    },

    read(fd, buffer, offset, length, position, cb) {
      try {
        const handle = handles.get(fd);
        if (!handle) throw fail("EBADF", "read", String(fd));
        const node = nodes.get(handle.path);
        if (node.dir) throw fail("EISDIR", "read", handle.path);
        const at = position === null || position === undefined ? handle.pos : Number(position);
        const n = Math.max(0, Math.min(length, node.data.length - at));
        if (n > 0) buffer.set(node.data.subarray(at, at + n), offset);
        if (position === null || position === undefined) handle.pos = at + n;
        reply(cb, null, n);
      } catch (err) {
        reply(cb, err);
      }
    },

    write(fd, buffer, offset, length, position, cb) {
      try {
        if (fd === 1 || fd === 2) {
          const bytes = buffer.subarray(offset, offset + length);
          if (fd === 1) stdout += stdoutDecoder.decode(bytes, { stream: true });
          else stderr += stderrDecoder.decode(bytes, { stream: true });
          return reply(cb, null, length);
        }
        const handle = handles.get(fd);
        if (!handle) throw fail("EBADF", "write", String(fd));
        const node = nodes.get(handle.path);
        if (node.dir) throw fail("EISDIR", "write", handle.path);
        const bytes = buffer.subarray(offset, offset + length);
        const at =
          position === null || position === undefined
            ? handle.flags & O_APPEND
              ? node.data.length
              : handle.pos
            : Number(position);
        writeAt(node, bytes, at);
        if (position === null || position === undefined) handle.pos = at + length;
        reply(cb, null, length);
      } catch (err) {
        reply(cb, err);
      }
    },

    // The runtime writes its own output (panics, runtime.print) through here.
    writeSync(fd, buf) {
      if (fd === 1) stdout += stdoutDecoder.decode(buf, { stream: true });
      else stderr += stderrDecoder.decode(buf, { stream: true });
      return buf.length;
    },

    mkdir(path, mode, cb) {
      try {
        mkdirOne(normalize(path), "mkdir");
        reply(cb, null);
      } catch (err) {
        reply(cb, err);
      }
    },

    rmdir(path, cb) {
      try {
        const { path: abs, node } = lookup(path, "rmdir");
        if (!node.dir) throw fail("ENOTDIR", "rmdir", abs);
        for (const key of nodes.keys()) {
          if (key !== abs && key.startsWith(abs + "/")) throw fail("ENOTEMPTY", "rmdir", abs);
        }
        nodes.delete(abs);
        reply(cb, null);
      } catch (err) {
        reply(cb, err);
      }
    },

    unlink(path, cb) {
      try {
        const { path: abs, node } = lookup(path, "unlink");
        if (node.dir) throw fail("EISDIR", "unlink", abs);
        nodes.delete(abs);
        reply(cb, null);
      } catch (err) {
        reply(cb, err);
      }
    },

    rename(from, to, cb) {
      try {
        const src = normalize(from);
        const dst = normalize(to);
        const node = nodes.get(src);
        if (!node) throw fail("ENOENT", "rename", src);
        if (!nodes.has(parentOf(dst))) throw fail("ENOENT", "rename", dst);
        nodes.delete(src);
        nodes.set(dst, node);
        for (const key of [...nodes.keys()]) {
          if (!key.startsWith(src + "/")) continue;
          nodes.set(dst + key.slice(src.length), nodes.get(key));
          nodes.delete(key);
        }
        reply(cb, null);
      } catch (err) {
        reply(cb, err);
      }
    },

    truncate(path, length, cb) {
      try {
        const { path: abs, node } = lookup(path, "truncate");
        if (node.dir) throw fail("EISDIR", "truncate", abs);
        resize(node, Number(length));
        reply(cb, null);
      } catch (err) {
        reply(cb, err);
      }
    },

    ftruncate(fd, length, cb) {
      try {
        const handle = handles.get(fd);
        if (!handle) throw fail("EBADF", "ftruncate", String(fd));
        resize(nodes.get(handle.path), Number(length));
        reply(cb, null);
      } catch (err) {
        reply(cb, err);
      }
    },

    // Everything below is a no-op or unsupported: this filesystem has no
    // permissions, no links and nothing to flush.
    fsync(fd, cb) {
      reply(cb, null);
    },
    fdatasync(fd, cb) {
      reply(cb, null);
    },
    chmod(path, mode, cb) {
      reply(cb, null);
    },
    fchmod(fd, mode, cb) {
      reply(cb, null);
    },
    chown(path, uid, gid, cb) {
      reply(cb, null);
    },
    fchown(fd, uid, gid, cb) {
      reply(cb, null);
    },
    lchown(path, uid, gid, cb) {
      reply(cb, null);
    },
    utimes(path, atime, mtime, cb) {
      reply(cb, null);
    },
    readlink(path, cb) {
      reply(cb, fail("EINVAL", "readlink", normalize(path)));
    },
    link(from, to, cb) {
      reply(cb, fail("EPERM", "link", normalize(from)));
    },
    symlink(target, path, cb) {
      reply(cb, fail("EPERM", "symlink", normalize(path)));
    },
  };

  globalThis.path = {
    // syscall/fs_js.go canonicalises every opened file through this, and stores
    // the result as the file's name, so it has to produce a clean absolute path.
    resolve(...segments) {
      return normalize(segments.join("/"));
    },
  };

  globalThis.process = {
    env: {},
    argv: [],
    getuid: () => 0,
    getgid: () => 0,
    geteuid: () => 0,
    getegid: () => 0,
    getgroups: () => [0],
    pid: -1,
    ppid: -1,
    umask: () => 0o022,
    cwd: () => "/",
    chdir: () => {},
    exit: () => {},
  };

  // os.TempDir() has no $TMPDIR to read, so it falls back to /tmp.
  mkdirAll("/tmp");

  globalThis.__vfs = {
    stdout: () => stdout,
    stderr: () => stderr,
    paths: () => [...nodes.keys()].sort(),
    read: (path) => {
      const node = nodes.get(normalize(path));
      return node && !node.dir ? node.data : null;
    },
  };
})();
