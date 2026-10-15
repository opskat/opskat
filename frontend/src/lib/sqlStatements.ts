// 方言无关的 SQL 语句切分与危险语句判断。只识别引号、注释、`$tag$` 块与分号，
// 不按驱动分支；SQL Server 的 GO 批分隔符不识别。

export interface SqlStatement {
  /** 语句文本（去掉首尾空白与分号，保留内部注释）。 */
  text: string;
  /** 在完整文本中的起始字符偏移（含）。 */
  start: number;
  /** 在完整文本中的结束字符偏移（不含）。 */
  end: number;
}

const COMMENT_FILL = " ";
const QUOTED_FILL = "q";

function fill(ch: string, from: number, to: number, src: string): string {
  // 保留换行，使行结构不变；其余字符替换成填充字符。
  let out = "";
  for (let i = from; i < to; i++) out += src[i] === "\n" ? "\n" : ch;
  return out;
}

function dollarTagAt(sql: string, i: number): string | null {
  if (sql[i] !== "$") return null;
  if (i > 0 && /[A-Za-z0-9_$]/.test(sql[i - 1])) return null;
  const m = /^\$([A-Za-z_][A-Za-z0-9_]*)?\$/.exec(sql.slice(i, i + 130));
  return m ? m[0] : null;
}

/**
 * 返回与原文等长的掩码：注释变空白，引号 / 标识符 / `$$` 块的内容变 `q`，
 * 其余（含分号）原样保留。分号与关键字判断都在掩码上做，所以不会被字符串或注释干扰。
 * 未闭合的引号、注释一直延伸到文本末尾。
 */
function maskNonCode(sql: string): string {
  let out = "";
  let i = 0;
  const n = sql.length;
  while (i < n) {
    const c = sql[i];
    const next = sql[i + 1];
    let end = -1;
    let ch = QUOTED_FILL;
    if (c === "-" && next === "-") {
      const nl = sql.indexOf("\n", i);
      end = nl < 0 ? n : nl;
      ch = COMMENT_FILL;
    } else if (c === "/" && next === "*") {
      const close = sql.indexOf("*/", i + 2);
      end = close < 0 ? n : close + 2;
      ch = COMMENT_FILL;
    } else if (c === "'" || c === '"' || c === "`") {
      end = i + 1;
      while (end < n) {
        if (c !== "`" && sql[end] === "\\") {
          end += 2;
        } else if (sql[end] === c) {
          if (sql[end + 1] === c) end += 2;
          else break;
        } else {
          end++;
        }
      }
      end = Math.min(end + 1, n);
    } else if (c === "[") {
      end = i + 1;
      while (end < n) {
        if (sql[end] === "]") {
          if (sql[end + 1] === "]") end += 2;
          else break;
        } else {
          end++;
        }
      }
      end = Math.min(end + 1, n);
    } else {
      const tag = dollarTagAt(sql, i);
      if (tag) {
        const close = sql.indexOf(tag, i + tag.length);
        end = close < 0 ? n : close + tag.length;
      }
    }
    if (end < 0) {
      out += c;
      i++;
    } else {
      out += fill(ch, i, end, sql);
      i = end;
    }
  }
  return out;
}

interface Fragment {
  start: number;
  end: number;
}

/** 按掩码里的分号切片，丢弃只含空白与注释的片段，并把首尾空白收掉。 */
function fragments(masked: string): Fragment[] {
  const out: Fragment[] = [];
  let from = 0;
  const push = (to: number) => {
    const m = /\S/.exec(masked.slice(from, to));
    if (m) {
      let end = to;
      while (/\s/.test(masked[end - 1])) end--;
      out.push({ start: from + m.index, end });
    }
  };
  for (let i = 0; i < masked.length; i++) {
    if (masked[i] === ";") {
      push(i);
      from = i + 1;
    }
  }
  push(masked.length);
  return out;
}

export function splitStatements(sql: string): SqlStatement[] {
  return fragments(maskNonCode(sql)).map(({ start, end }) => ({ text: sql.slice(start, end), start, end }));
}

/**
 * offset 是完整编辑器文本里的字符偏移。语句之间的空白、分号、最后一个分号之后的
 * 空白或注释都归上一条语句；第一条之前的内容归第一条。没有语句或偏移越界返回 null。
 */
export function statementAt(sql: string, offset: number): SqlStatement | null {
  if (offset < 0 || offset > sql.length) return null;
  const all = splitStatements(sql);
  if (all.length === 0) return null;
  let hit = all[0];
  for (const s of all) {
    if (s.start <= offset) hit = s;
  }
  return hit;
}

/** 任一语句以 DELETE / DROP / TRUNCATE / ALTER 开头，或是不含 WHERE 的 UPDATE。 */
export function isDangerousSql(sql: string): boolean {
  const masked = maskNonCode(sql);
  return fragments(masked).some(({ start, end }) => {
    const stmt = masked.slice(start, end).toUpperCase();
    if (/^(DELETE|DROP|TRUNCATE|ALTER)\b/.test(stmt)) return true;
    return /^UPDATE\b/.test(stmt) && !/\bWHERE\b/.test(stmt);
  });
}
