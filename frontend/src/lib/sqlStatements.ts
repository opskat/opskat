// SQL 语句切分与危险语句判断。默认的切分方言无关：只识别引号、注释、`$tag$` 块与分号，
// 不按驱动分支；SQL Server 的 GO 批分隔符不识别。声明了语句切分分类的驱动自己切分
// （Query.ClassifySQLStatements），它的结果经 fromDriverStatements 换成编辑器位置后与默认切分
// 同样定位当前语句，危险判断两边共用 isDangerousStatement。

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
 * backslashEscapes：引号内的 `\` 是否转义下一个字符（MySQL 是，PostgreSQL / SQLite / SQL Server 不是）。
 */
function maskNonCode(sql: string, backslashEscapes = true): string {
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
        if (backslashEscapes && c !== "`" && sql[end] === "\\") {
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

// 存储过程 / 函数 / 触发器 / 事件的定义：体内的分号不结束语句（MySQL、SQLite、SQL Server
// 的 BEGIN … END 体；PostgreSQL 的 $$ 体已在掩码里）。只在这类定义里数块层级，所以单独的
// BEGIN（开启事务）照常按分号切分。
const ROUTINE_DEFINITION =
  /\s*(CREATE|ALTER)\s+(OR\s+(REPLACE|ALTER)\s+)?(DEFINER\s*=\s*\S+\s+)?((AGGREGATE|TEMP|TEMPORARY|CONSTRAINT)\s+)?(PROCEDURE|PROC|FUNCTION|TRIGGER|EVENT)\b/iy;
// 跟在 END 后面、各自配对的结束词：IF / LOOP / WHILE / REPEAT / FOR 的开头没有计入层级，
// CASE / TRY / CATCH 的开头（CASE、BEGIN TRY、BEGIN CATCH）计入了。
const END_OF_UNCOUNTED = /\s+(IF|LOOP|WHILE|REPEAT|FOR)\b/iy;
const END_OF_COUNTED = /\s+(CASE|TRY|CATCH)\b/iy;
// BEGIN TRAN / TRANSACTION / DISTRIBUTED 是事务语句，没有对应的 END。
const BEGIN_TRANSACTION = /\s+(TRAN|TRANSACTION|DISTRIBUTED)\b/iy;

function stickyMatch(re: RegExp, s: string, at: number): number {
  re.lastIndex = at;
  return re.test(s) ? re.lastIndex : -1;
}

/** 从 from 开始的语句在哪个分号处结束（没有就到文本末尾）。 */
function statementEnd(masked: string, from: number): number {
  if (stickyMatch(ROUTINE_DEFINITION, masked, from) < 0) {
    const semi = masked.indexOf(";", from);
    return semi < 0 ? masked.length : semi;
  }
  const token = /[\w$]+|;/g;
  token.lastIndex = from;
  let depth = 0;
  for (let m = token.exec(masked); m; m = token.exec(masked)) {
    const word = m[0].toUpperCase();
    if (word === ";") {
      if (depth === 0) return m.index;
    } else if (word === "CASE") {
      depth++;
    } else if (word === "BEGIN") {
      if (stickyMatch(BEGIN_TRANSACTION, masked, token.lastIndex) < 0) depth++;
    } else if (word === "END") {
      const uncounted = stickyMatch(END_OF_UNCOUNTED, masked, token.lastIndex);
      if (uncounted >= 0) {
        token.lastIndex = uncounted;
      } else {
        const counted = stickyMatch(END_OF_COUNTED, masked, token.lastIndex);
        if (counted >= 0) token.lastIndex = counted;
        depth = Math.max(0, depth - 1);
      }
    }
  }
  return masked.length;
}

/** 按掩码里结束语句的分号切片，丢弃只含空白与注释的片段，并把首尾空白收掉。 */
function fragments(masked: string): Fragment[] {
  const out: Fragment[] = [];
  const push = (from: number, to: number) => {
    const m = /\S/.exec(masked.slice(from, to));
    if (m) {
      let end = to;
      while (/\s/.test(masked[end - 1])) end--;
      out.push({ start: from + m.index, end });
    }
  };
  for (let from = 0; from <= masked.length;) {
    const end = statementEnd(masked, from);
    push(from, end);
    from = end + 1;
  }
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
  return statementAtOffset(splitStatements(sql), sql.length, offset);
}

/**
 * 在按顺序排列的语句 all（来自长 textLength 的文本）里找 offset 所在的那条，归属规则同 statementAt。
 * 默认切分与驱动切分都经它定位当前语句。
 */
export function statementAtOffset<T extends SqlStatement>(all: T[], textLength: number, offset: number): T | null {
  if (offset < 0 || offset > textLength || all.length === 0) return null;
  let hit = all[0];
  for (const s of all) {
    if (s.start <= offset) hit = s;
  }
  return hit;
}

/** 不带 WHERE 的 UPDATE 的危险原因（Go 侧 dbdriver.ReasonNoWhereUpdate）。 */
const NO_WHERE_UPDATE = "no_where_update";
/** 不带 WHERE 的 DELETE 的危险原因（Go 侧 dbdriver.ReasonNoWhereDelete）。 */
const NO_WHERE_DELETE = "no_where_delete";

/** 一条语句的类型与危险标记：驱动给出的，或默认切分从语句开头推出的。 */
export interface StatementKind {
  /** 语句类型，大写（如 SELECT、DROP TABLE、驱动自己的 PL/SQL 块类型）。 */
  type: string;
  dangerous: boolean;
  /** 危险原因（Go 侧 dbdriver.DangerReasons 之一）；不危险时没有。 */
  reason?: string;
}

/** 驱动切分出的一条语句（Go 侧 dbdriver.Statement）：start / end 是 UTF-8 字节偏移。 */
export interface DriverStatement extends StatementKind {
  start: number;
  end: number;
  text: string;
}

/** 驱动切分出的一条语句，偏移已换成编辑器的字符（UTF-16）偏移。 */
export type ClassifiedSqlStatement = SqlStatement & StatementKind;

/**
 * 查询面板确认框的判定：DELETE / DROP / TRUNCATE / ALTER 类语句，或标了不带 WHERE 的 UPDATE /
 * DELETE 的语句（驱动自己的类型如 PL/SQL 块也按原因算）；预编译执行与调用存储过程不在此列
 * （与按分号切分时一致）。
 */
export function isDangerousStatement(s: StatementKind): boolean {
  if (/^(DELETE|DROP|TRUNCATE|ALTER)\b/.test(s.type)) return true;
  return s.dangerous && (s.reason === NO_WHERE_UPDATE || s.reason === NO_WHERE_DELETE);
}

/** 一个 UTF-16 码点在 UTF-8 里占的字节数；孤立的代理项按 Go 的 U+FFFD 算 3 字节。 */
function utf8Length(codePoint: number): number {
  if (codePoint < 0x80) return 1;
  if (codePoint < 0x800) return 2;
  if (codePoint < 0x10000) return 3;
  return 4;
}

/**
 * 把驱动给的语句（sql 里的 UTF-8 字节偏移）换成编辑器的字符偏移。驱动的输出已由宿主校验：
 * 按顺序、不重叠、不切开字符，所以一趟从头走到尾即可。
 */
export function fromDriverStatements(sql: string, statements: DriverStatement[]): ClassifiedSqlStatement[] {
  let index = 0;
  let bytes = 0;
  const toIndex = (target: number) => {
    while (bytes < target) {
      const cp = sql.codePointAt(index)!;
      bytes += utf8Length(cp);
      index += cp > 0xffff ? 2 : 1;
    }
    return index;
  };
  return statements.map(({ start, end, text, type, dangerous, reason }) => ({
    start: toIndex(start),
    end: toIndex(end),
    text,
    type,
    dangerous,
    reason,
  }));
}

/** 从掩码后的语句开头推出它的类型（首个词），以及不带 WHERE 的 UPDATE 的危险标记。 */
function kindOf(maskedStatement: string): StatementKind {
  const stmt = maskedStatement.toUpperCase();
  const type = /^\w*/.exec(stmt)![0];
  const noWhereUpdate = type === "UPDATE" && !/\bWHERE\b/.test(stmt);
  return noWhereUpdate ? { type, dangerous: true, reason: NO_WHERE_UPDATE } : { type, dangerous: false };
}

/**
 * 任一语句以 DELETE / DROP / TRUNCATE / ALTER 开头，或是不含 WHERE 的 UPDATE。
 * 反斜杠是否转义因方言而异，两种读法都判断一次，任一读法危险即危险：
 * 否则 `SELECT 'C:\'; DROP TABLE x` 在 PostgreSQL 上会被当成一条未闭合的字符串而漏掉 DROP。
 */
export function isDangerousSql(sql: string): boolean {
  return [true, false].some((backslashEscapes) => {
    const masked = maskNonCode(sql, backslashEscapes);
    return fragments(masked).some(({ start, end }) => isDangerousStatement(kindOf(masked.slice(start, end))));
  });
}
