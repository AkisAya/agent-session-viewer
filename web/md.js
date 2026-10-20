// md.js — a tiny dependency-free Markdown renderer for untrusted agent output.
// Loaded via a plain <script> tag before app.js; exposes window.md.render(src).
(function () {
  // esc escapes the five dangerous characters in text nodes and attribute values.
  const esc = (s) =>
    String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

  // safeUrl returns the url when it is safe to emit, otherwise "".
  // The probe strips every whitespace/control char first so tricks like
  // " \tJAVA\nSCRIPT:alert(1)" are still recognized as a javascript: url.
  function safeUrl(raw) {
    const url = String(raw || "").trim();
    const probe = url.replace(/[\s\u0000-\u001f\u007f]/g, "").toLowerCase();
    if (!probe) return "";
    if (/^[a-z][a-z0-9+.-]*:/.test(probe)) {
      // A scheme is present: only http(s) and mailto survive.
      return /^(https?|mailto):/.test(probe) ? url : "";
    }
    // No scheme: "#", "/", "./", "../" and plain relative paths are allowed.
    return url;
  }

  // langCls sanitizes a fence info string to a safe token for class names.
  const langCls = (lang) => (String(lang || "").match(/[a-zA-Z0-9_+-]{1,20}/) || [""])[0];

  // codeHtml renders one extracted fenced code block.
  function codeHtml(lang, code) {
    const cls = langCls(lang);
    const head = cls
      ? `<div class="md-code-head"><span class="md-lang">${esc(cls)}</span><button type="button" class="code-copy">复制</button></div>`
      : `<div class="md-code-head"><button type="button" class="code-copy">复制</button></div>`;
    const codeCls = cls ? `md-code md-lang-${esc(cls)}` : "md-code";
    return `<pre class="md-pre">${head}<code class="${codeCls}">${esc(code)}</code></pre>`;
  }

  /* ---------- inline ---------- */
  // inline parses inline markdown in already-line-oriented text. It escapes
  // everything first, stashes generated HTML in \u0000I<n>\u0000 slots so later
  // passes never re-process it, and turns single newlines into <br>.
  function inline(raw) {
    const slots = [];
    const stash = (html) => {
      slots.push(html);
      return "\u0000I" + (slots.length - 1) + "\u0000";
    };
    let s = esc(raw);

    // Code spans run FIRST so their content is never touched by other rules.
    s = s.replace(/(`+)([^`\n]+?)\1/g, (m, ticks, code) => stash("<code>" + code + "</code>"));

    // Images: ![alt](url). They are not loaded automatically: agent output can
    // point at a remote host, and opening a transcript should not leak the
    // reader's IP. The detail view turns a placeholder into an <img> on click.
    s = s.replace(/!\[([^\]]*)\]\(([^()\s]*(?:\([^()\s]*\)[^()\s]*)*)\)/g, (m, alt, target) => {
      const url = safeUrl(target);
      if (!url) return alt.replace(/\n/g, " ");
      const a = alt.replace(/\u0000I\d+\u0000/g, "").replace(/\n/g, " ") || "图片";
      return stash(`<span class="md-img-blocked" role="button" tabindex="0" data-src="${esc(url)}" title="图片默认不加载，点击显示">${esc(a)}</span>`);
    });

    // Links: [text](url); unsafe urls render the label as plain text.
    s = s.replace(/\[([^\]]+)\]\(([^()\s]*(?:\([^()\s]*\)[^()\s]*)*)\)/g, (m, label, target) => {
      const url = safeUrl(target);
      if (!url) return label;
      return stash(`<a href="${esc(url)}" target="_blank" rel="noopener noreferrer">${label}</a>`);
    });

    // Strikethrough, bold, then italic (bold must precede italic).
    s = s.replace(/~~([^\n]+?)~~/g, "<del>$1</del>");
    s = s.replace(/\*\*([^\n]+?)\*\*/g, "<strong>$1</strong>");
    s = s.replace(/\*([^\n]+?)\*/g, "<em>$1</em>");
    // Underscore italic requires word boundaries so snake_case stays intact.
    s = s.replace(/(^|[^\w])_([^\n_]+)_(?!\w)/g, "$1<em>$2</em>");

    // Autolink bare urls; stop at whitespace and trailing punctuation.
    s = s.replace(/(^|[\s(（"'\[=])(https?:\/\/[^\s<>"']+)/g, (m, pre, url) => {
      const clean = url.replace(/[.,;:!?)\]}]+$/, "");
      if (!clean) return m;
      const link = stash(`<a href="${esc(clean)}" target="_blank" rel="noopener noreferrer">${clean}</a>`);
      return pre + link + url.slice(clean.length);
    });

    return s.replace(/\u0000I(\d+)\u0000/g, (m, n) => slots[Number(n)] || "").replace(/\n/g, "<br>\n");
  }

  /* ---------- tables ---------- */
  // isSepRow recognizes a GFM separator row like |---|:---:|.
  function isSepRow(line) {
    return /^\s*\|?\s*:?-{1,}:?\s*(\|\s*:?-{1,}:?\s*)*\|?\s*$/.test(line) && line.indexOf("-") >= 0;
  }

  // splitCells trims the outer pipes and splits the remaining row on pipes.
  function splitCells(row) {
    let r = row.trim();
    if (r.startsWith("|")) r = r.slice(1);
    if (r.endsWith("|")) r = r.slice(0, -1);
    return r.split("|").map((c) => c.trim());
  }

  // alignOf maps a separator cell to a css alignment value, or "".
  function alignOf(cell) {
    const l = cell.startsWith(":");
    const r = cell.endsWith(":");
    if (l && r) return "center";
    if (r) return "right";
    if (l) return "left";
    return "";
  }

  function parseTable(lines, start) {
    const heads = splitCells(lines[start]);
    const aligns = splitCells(lines[start + 1]).map(alignOf);
    const rows = [];
    for (let i = start + 2; i < lines.length && lines[i].trim() && lines[i].indexOf("|") >= 0; i++) {
      rows.push(splitCells(lines[i]));
    }
    const th = heads
      .map((c, n) => `<th${aligns[n] ? ` style="text-align:${aligns[n]}"` : ""}>${inline(c)}</th>`)
      .join("");
    const body = rows
      .map((r) => "<tr>" + heads.map((_, n) => `<td${aligns[n] ? ` style="text-align:${aligns[n]}"` : ""}>${inline(r[n] || "")}</td>`).join("") + "</tr>")
      .join("");
    return `<div class="md-table-wrap"><table class="md-table"><thead><tr>${th}</tr></thead><tbody>${body}</tbody></table></div>`;
  }

  /* ---------- lists ---------- */
  const LIST_RE = /^( {0,99})([-*+]|\d{1,9}[.)]) +(.*)$/;

  // parseList consumes one list (top-level indent < 2). Nesting is supported for
  // ONE level (depth 0 -> 1); deeper indentation is folded into item text.
  function parseList(lines, start, ordered, depth) {
    const items = [];
    let i = start;
    while (i < lines.length) {
      const m = LIST_RE.exec(lines[i]);
      if (m && m[1].length < 2 && ordered === /\d/.test(m[2])) {
        items.push({ text: [m[3]], nested: "" });
        i++;
      } else if (m && m[1].length >= 2 && items.length) {
        if (depth >= 5) {
          // Too deep: flatten into the current item as plain text.
          items[items.length - 1].text.push(lines[i].trim());
          i++;
        } else {
          // Collect the nested list region, strip 2 spaces, re-parse one level down.
          const sub = [];
          while (i < lines.length) {
            const nm = LIST_RE.exec(lines[i]);
            if (nm && nm[1].length >= 2) {
              sub.push(lines[i].slice(2));
              i++;
            } else if (!nm && lines[i].trim() && /^ {2,}\S/.test(lines[i])) {
              sub.push(lines[i].slice(2));
              i++;
            } else break;
          }
          items[items.length - 1].nested = parseList(sub, 0, /\d/.test(LIST_RE.exec(sub[0])[2]), depth + 1).html;
        }
      } else if (!m && items.length && lines[i].trim() && /^ {2,}\S/.test(lines[i])) {
        // Indented continuation of the current item.
        items[items.length - 1].text.push(lines[i].slice(2));
        i++;
      } else if (!m && !lines[i].trim()) {
        // A blank line continues the list only when a list item follows.
        let j = i + 1;
        while (j < lines.length && !lines[j].trim()) j++;
        if (j < lines.length && LIST_RE.exec(lines[j])) {
          i = j;
        } else break;
      } else break;
    }
    const tag = ordered ? "ol" : "ul";
    const html = items
      .map((it) => {
        const raw = it.text.join("\n");
        const task = /^\s*\[([ xX])\]\s+([\s\S]*)$/.exec(raw);
        const box = task
          ? `<span class="md-task${task[1].toLowerCase() === "x" ? " done" : ""}" aria-hidden="true"></span>`
          : "";
        return `<li${task ? ' class="task"' : ""}>${box}${inline(task ? task[2] : raw)}${it.nested}</li>`;
      })
      .join("");
    return { html: `<${tag}>${html}</${tag}>`, next: i };
  }

  /* ---------- blocks ---------- */
  const CODE_PH_RE = /^\u0000CB(\d+)\u0000$/;
  const MAX_DEPTH = 5;

  // isBlockStart reports whether line begins a new block (used to end paragraphs).
  function isBlockStart(line, next) {
    return (
      /^ {0,3}(#{1,6})( +|$)/.test(line) ||
      /^ {0,3}([-*_])(?: *\1){2,} *$/.test(line) ||
      /^ {0,3}>/.test(line) ||
      LIST_RE.test(line) ||
      CODE_PH_RE.test(line.trim()) ||
      (line.indexOf("|") >= 0 && next !== undefined && isSepRow(next))
    );
  }

  // blocks turns lines into an html string; depth guards quote/list recursion.
  function blocks(lines, depth) {
    const out = [];
    let i = 0;
    while (i < lines.length) {
      const line = lines[i];
      const next = lines[i + 1];
      if (!line.trim()) {
        i++;
        continue;
      }
      // Fenced code placeholder emitted raw (already escaped during extraction).
      const ph = CODE_PH_RE.exec(line.trim());
      if (ph) {
        out.push(CODE[Number(ph[1])]);
        i++;
        continue;
      }
      // ATX heading.
      const h = /^ {0,3}(#{1,6})(?: +(.*))?$/.exec(line);
      if (h) {
        const lvl = h[1].length;
        out.push(`<h${lvl} class="md-h${lvl}">${inline((h[2] || "").replace(/ +#+$/, ""))}</h${lvl}>`);
        i++;
        continue;
      }
      // Horizontal rule (alone on the line).
      if (/^ {0,3}([-*_])(?: *\1){2,} *$/.test(line)) {
        out.push("<hr>");
        i++;
        continue;
      }
      // Blockquote: strip one ">" level, recurse for nested quotes.
      if (/^ {0,3}>/.test(line)) {
        const q = [];
        while (i < lines.length && (/^ {0,3}>/.test(lines[i]) || CODE_PH_RE.test(lines[i].trim()))) {
          q.push(lines[i].replace(/^ {0,3}> ?/, ""));
          i++;
        }
        out.push(
          depth >= MAX_DEPTH ? inline(q.join("\n")) : `<blockquote class="md-quote">${blocks(q, depth + 1)}</blockquote>`
        );
        continue;
      }
      // Lists (ordered or unordered).
      const lm = LIST_RE.exec(line);
      if (lm && lm[1].length < 2) {
        const r = parseList(lines, i, /\d/.test(lm[2]), 0);
        out.push(r.html);
        i = r.next;
        continue;
      }
      // GFM pipe table: header row + separator row.
      if (line.indexOf("|") >= 0 && next !== undefined && isSepRow(next)) {
        out.push(parseTable(lines, i));
        i += 2;
        while (i < lines.length && lines[i].trim() && lines[i].indexOf("|") >= 0) i++;
        continue;
      }
      // Paragraph: hard-wrapped prose, single newlines become <br>.
      const para = [line];
      i++;
      while (i < lines.length && lines[i].trim() && !isBlockStart(lines[i], lines[i + 1])) {
        para.push(lines[i]);
        i++;
      }
      out.push(`<p>${inline(para.join("\n"))}</p>`);
    }
    return out.join("");
  }

  /* ---------- fences ---------- */
  // CODE holds extracted fenced blocks; placeholders \u0000CB<n>\u0000 mark their spot.
  let CODE = [];

  // extractFences pulls ``` / ~~~ blocks out of the source before block parsing,
  // so their content is never inline-parsed. Unterminated fences run to EOF.
  function extractFences(src) {
    const lines = src.split("\n");
    const kept = [];
    let i = 0;
    while (i < lines.length) {
      // Allow an indent/quote prefix so "> ```go" fences inside quotes work too.
      const open = /^([ \t]*(?:>[ \t]?)*)(`{3,}|~{3,})[ \t]*([^`\n]*)$/.exec(lines[i]);
      if (open) {
        const prefix = open[1];
        const mark = open[2][0];
        const lang = open[3].trim();
        // The closing fence tolerates any indent but keeps the same quote depth.
        const quotes = (prefix.match(/>/g) || []).length;
        const closeRe = new RegExp("^[ \\t]*(?:>[ \\t]?){0," + quotes + "}" + mark + "{3,}[ \\t]*$");
        const body = [];
        i++;
        while (i < lines.length && !closeRe.test(lines[i])) {
          body.push(lines[i].startsWith(prefix) ? lines[i].slice(prefix.length) : lines[i]);
          i++;
        }
        if (i < lines.length) i++; // skip the closing fence
        CODE.push(codeHtml(lang, body.join("\n")));
        kept.push("\u0000CB" + (CODE.length - 1) + "\u0000");
      } else {
        kept.push(lines[i]);
        i++;
      }
    }
    return kept.join("\n");
  }

  /* ---------- api ---------- */
  function render(src) {
    CODE = [];
    let s = String(src ?? "");
    s = s.replace(/\r\n?/g, "\n").replace(/[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f]/g, "");
    s = extractFences(s);
    return blocks(s.split("\n"), 0).replace(/\u0000CB(\d+)\u0000/g, (m, n) => CODE[Number(n)] || "");
  }

  window.md = { render };
})();
