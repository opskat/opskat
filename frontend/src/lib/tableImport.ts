export type Delimiter = "," | "\t";
export type ImportNullStrategy = "empty-is-empty-string" | "empty-is-null" | "literal-null";
export type ImportDataFormat = "text" | "csv" | "json" | "xml" | "xlsx";
export type ImportFieldDelimiter = "," | "\t" | ";" | "|" | " ";
export type ImportMode = "append" | "update" | "append-update" | "append-skip" | "delete" | "copy";
export type ImportRecordDelimiter = "auto" | "lf" | "cr" | "crlf";
export type ImportTextQualifier = '"' | "'" | "none";
export type ImportDateOrder = "dmy" | "mdy" | "ymd";
export type ImportDateTimeOrder = "date-time" | "time-date";
export type ImportBinaryEncoding = "base64" | "hex";

export interface ImportAdvancedOptions {
  extendedInsert?: boolean;
  maxStatementSizeKb?: number;
  emptyStringAsNull?: boolean;
  ignoreForeignKeyConstraint?: boolean;
}

export interface ImportValueConversionOptions {
  dateOrder?: ImportDateOrder;
  dateTimeOrder?: ImportDateTimeOrder;
  dateDelimiter?: string;
  yearDelimiter?: string;
  timeDelimiter?: string;
  decimalSymbol?: string;
  binaryEncoding?: ImportBinaryEncoding;
}

export interface ParsedDelimitedTable {
  headers: string[];
  rows: string[][];
}

export interface ParseImportSourceTextArgs {
  text: string;
  format: ImportDataFormat;
  fieldDelimiter?: ImportFieldDelimiter;
  recordDelimiter?: ImportRecordDelimiter;
  textQualifier?: ImportTextQualifier;
  fixedWidth?: boolean;
  fieldNameRowEnabled?: boolean;
  fieldNameRow?: number;
  dataStartRow?: number;
  dataEndRow?: number;
}

/** 驱动按自己的类型系统给出的列类别（后端 dbdriver.ColumnCategory），决定源文件里的值怎么转换。 */
export type ImportColumnCategory = "text" | "number" | "date" | "datetime" | "binary";

/** 转换好的一个值（后端 dbdriver.ImportValue）：文本、空值，或按十六进制给出的二进制字节。 */
export type ImportValue = { kind: "text"; text: string } | { kind: "null" } | { kind: "binary"; hex: string };

/** 发给驱动的导入请求（后端 dbdriver.ImportRequest）；语句由驱动生成（Query.GetImportStatements）。 */
export interface ImportIntent {
  mode: ImportMode;
  columns: string[];
  keys: string[];
  rows: ImportValue[][];
  extendedInsert: boolean;
  maxStatementSizeKb: number;
}

export interface BuildImportIntentArgs {
  headers: string[];
  rows: string[][];
  mapping: Record<string, string>;
  nullStrategy: ImportNullStrategy;
  mode?: ImportMode;
  /** 匹配已有行的键列。 */
  keys?: string[];
  advancedOptions?: ImportAdvancedOptions;
  columnCategories?: Record<string, ImportColumnCategory>;
  conversionOptions?: ImportValueConversionOptions;
}

export function detectDelimiter(text: string): Delimiter {
  const firstLine = text.split(/\r?\n/, 1)[0] ?? "";
  const tabs = (firstLine.match(/\t/g) ?? []).length;
  const commas = (firstLine.match(/,/g) ?? []).length;
  return tabs > commas ? "\t" : ",";
}

interface NewlineMatch {
  match: boolean;
  len: number;
}

function newlineAt(text: string, i: number, mode: ImportRecordDelimiter): NewlineMatch {
  const ch = text[i];
  const next = text[i + 1];
  if (mode === "lf") return { match: ch === "\n", len: 1 };
  if (mode === "cr") return { match: ch === "\r" && next !== "\n", len: 1 };
  if (mode === "crlf") return { match: ch === "\r" && next === "\n", len: 2 };
  if (ch === "\r" && next === "\n") return { match: true, len: 2 };
  if (ch === "\n" || ch === "\r") return { match: true, len: 1 };
  return { match: false, len: 0 };
}

interface ParseDelimitedRowsOptions {
  recordDelimiter?: ImportRecordDelimiter;
  textQualifier?: ImportTextQualifier;
  preserveEmptyRows?: boolean;
  preserveQuotesInUnquotedValues?: boolean;
}

function hasDelimitedClosingQualifier(
  text: string,
  openingIndex: number,
  qualifier: string,
  delimiter: ImportFieldDelimiter,
  recordDelimiter: ImportRecordDelimiter
): boolean {
  for (let i = openingIndex + 1; i < text.length; i++) {
    if (text[i] !== qualifier) continue;
    if (text[i + 1] === qualifier) {
      i++;
      continue;
    }
    const nextIndex = i + 1;
    return (
      nextIndex === text.length || text[nextIndex] === delimiter || newlineAt(text, nextIndex, recordDelimiter).match
    );
  }
  return false;
}

function parseDelimitedRows(
  text: string,
  delimiter: ImportFieldDelimiter = detectDelimiter(text),
  options: ParseDelimitedRowsOptions = {}
): string[][] {
  const recordDelimiter = options.recordDelimiter ?? "auto";
  const qualifier = options.textQualifier ?? '"';
  const useQualifier = qualifier !== "none";

  const rows: string[][] = [];
  let currentRow: string[] = [];
  let current = "";
  let inQuotes = false;
  let rowStarted = false;

  for (let i = 0; i < text.length; i++) {
    const ch = text[i];
    const next = text[i + 1];

    const canToggleQualifier =
      !options.preserveQuotesInUnquotedValues ||
      inQuotes ||
      (current.length === 0 && hasDelimitedClosingQualifier(text, i, qualifier, delimiter, recordDelimiter));
    if (useQualifier && ch === qualifier && canToggleQualifier) {
      rowStarted = true;
      if (inQuotes && next === qualifier) {
        current += qualifier;
        i++;
      } else {
        inQuotes = !inQuotes;
      }
      continue;
    }

    if (ch === delimiter && !inQuotes) {
      rowStarted = true;
      currentRow.push(current);
      current = "";
      continue;
    }

    if (!inQuotes) {
      const nl = newlineAt(text, i, recordDelimiter);
      if (nl.match) {
        i += nl.len - 1;
        currentRow.push(current);
        if (options.preserveEmptyRows || currentRow.some((cell) => cell !== "")) rows.push(currentRow);
        currentRow = [];
        current = "";
        rowStarted = false;
        continue;
      }
    }

    current += ch;
    rowStarted = true;
  }

  currentRow.push(current);
  if (currentRow.some((cell) => cell !== "") || (options.preserveEmptyRows && rowStarted)) rows.push(currentRow);

  return rows;
}

export function parseDelimitedText(text: string, delimiter: Delimiter = detectDelimiter(text)): ParsedDelimitedTable {
  const rows = parseDelimitedRows(text, delimiter);
  const headers = rows[0] ?? [];
  return { headers, rows: rows.slice(1) };
}

/**
 * Parses clipboard-style tab-separated text into rows of cells. Unlike
 * {@link parseDelimitedText} there is no header row and no delimiter detection: a pasted
 * table has no trustworthy first row, and a value containing commas must stay one cell.
 */
export function parseTabSeparatedRows(text: string): string[][] {
  return parseDelimitedRows(text, "\t", { preserveEmptyRows: true, preserveQuotesInUnquotedValues: true });
}

function normalizeCell(value: unknown): string {
  if (value == null) return "";
  if (typeof value === "string") return value;
  if (typeof value === "number" || typeof value === "boolean" || typeof value === "bigint") return String(value);
  return JSON.stringify(value);
}

function tableFromRecords(records: Record<string, unknown>[]): ParsedDelimitedTable {
  const headers: string[] = [];
  for (const record of records) {
    for (const key of Object.keys(record)) {
      if (!headers.includes(key)) headers.push(key);
    }
  }

  return {
    headers,
    rows: records.map((record) => headers.map((header) => normalizeCell(record[header]))),
  };
}

function parseJsonText(text: string): ParsedDelimitedTable {
  const data = JSON.parse(text) as unknown;
  let rows: unknown[] = [];

  if (Array.isArray(data)) {
    rows = data;
  } else if (data && typeof data === "object") {
    const firstArray = Object.values(data).find(Array.isArray);
    rows = firstArray ?? [data];
  }

  const records = rows.map((row) =>
    row && typeof row === "object" && !Array.isArray(row) ? row : { value: row }
  ) as Record<string, unknown>[];
  return tableFromRecords(records);
}

function elementChildren(element: Element): Element[] {
  return Array.from(element.children);
}

function elementToRecord(element: Element): Record<string, unknown> {
  const record: Record<string, unknown> = {};
  for (const attr of Array.from(element.attributes)) {
    record[`@${attr.name}`] = attr.value;
  }

  for (const child of elementChildren(element)) {
    const value = child.children.length > 0 ? elementToRecord(child) : (child.textContent?.trim() ?? "");
    if (record[child.tagName]) {
      const existing = record[child.tagName];
      record[child.tagName] = Array.isArray(existing) ? [...existing, value] : [existing, value];
    } else {
      record[child.tagName] = value;
    }
  }

  if (Object.keys(record).length === 0) record.value = element.textContent?.trim() ?? "";
  return record;
}

function parseXmlText(text: string): ParsedDelimitedTable {
  const parser = new DOMParser();
  const doc = parser.parseFromString(text, "application/xml");
  const parseError = doc.querySelector("parsererror");
  if (parseError) throw new Error(parseError.textContent ?? "Invalid XML");

  const root = doc.documentElement;
  if (!root) return { headers: [], rows: [] };

  const children = elementChildren(root);
  const byName = new Map<string, Element[]>();
  for (const child of children) {
    byName.set(child.tagName, [...(byName.get(child.tagName) ?? []), child]);
  }
  const repeated = Array.from(byName.values())
    .filter((items) => items.length > 1)
    .sort((a, b) => b.length - a.length)[0];
  const rowElements =
    repeated ?? (children.length > 0 && children.every((child) => child.children.length > 0) ? children : [root]);

  return tableFromRecords(rowElements.map(elementToRecord));
}

function parseFixedWidthRows(text: string): string[][] {
  return text
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => line.split(/\s{2,}|\t+/).filter(Boolean));
}

function applyRowOptions(rows: string[][], args: ParseImportSourceTextArgs): ParsedDelimitedTable {
  const hasHeader = args.fieldNameRowEnabled ?? true;
  const headerIndex = Math.max((args.fieldNameRow ?? 1) - 1, 0);
  const dataStartIndex = Math.max((args.dataStartRow ?? (hasHeader ? headerIndex + 2 : 1)) - 1, 0);
  const dataEndIndex = args.dataEndRow ? Math.max(args.dataEndRow, dataStartIndex + 1) : undefined;
  const maxColumns = Math.max(...rows.map((row) => row.length), 0);
  const headers = hasHeader
    ? (rows[headerIndex] ?? [])
    : Array.from({ length: maxColumns }, (_, index) => `Column ${index + 1}`);

  return {
    headers,
    rows: rows.slice(dataStartIndex, dataEndIndex),
  };
}

export function parseImportSourceText(args: ParseImportSourceTextArgs): ParsedDelimitedTable {
  if (args.format === "json") return parseJsonText(args.text);
  if (args.format === "xml") return parseXmlText(args.text);

  const rows = args.fixedWidth
    ? parseFixedWidthRows(args.text)
    : parseDelimitedRows(args.text, args.fieldDelimiter, {
        recordDelimiter: args.recordDelimiter,
        textQualifier: args.textQualifier,
      });
  return applyRowOptions(rows, args);
}

function importValue(cell: string, nullStrategy: ImportNullStrategy): unknown {
  if (nullStrategy === "empty-is-null" && cell === "") return null;
  if (nullStrategy === "literal-null" && cell.toUpperCase() === "NULL") return null;
  return cell;
}

const MONTH_NAMES: Record<string, number> = {
  jan: 1,
  january: 1,
  feb: 2,
  february: 2,
  mar: 3,
  march: 3,
  apr: 4,
  april: 4,
  may: 5,
  jun: 6,
  june: 6,
  jul: 7,
  july: 7,
  aug: 8,
  august: 8,
  sep: 9,
  sept: 9,
  september: 9,
  oct: 10,
  october: 10,
  nov: 11,
  november: 11,
  dec: 12,
  december: 12,
};

function pad(value: number, width: number): string {
  return String(value).padStart(width, "0");
}

function expandTwoDigitYear(text: string): number | null {
  const trimmed = text.trim();
  if (!/^\d+$/.test(trimmed)) return null;
  const n = Number(trimmed);
  if (trimmed.length <= 2) return n + (n < 70 ? 2000 : 1900);
  return n;
}

function parseMonth(text: string): number | null {
  const trimmed = text.trim().toLowerCase();
  if (trimmed in MONTH_NAMES) return MONTH_NAMES[trimmed];
  if (!/^\d+$/.test(trimmed)) return null;
  const n = Number(trimmed);
  if (n < 1 || n > 12) return null;
  return n;
}

function splitDateParts(
  text: string,
  order: ImportDateOrder,
  dateDelimiter: string,
  yearDelimiter?: string
): [string, string, string] | null {
  const trimmed = text.trim();
  if (!trimmed) return null;
  if (yearDelimiter && yearDelimiter !== dateDelimiter) {
    if (order === "ymd") {
      const idx = trimmed.indexOf(yearDelimiter);
      if (idx === -1) return null;
      const rest = trimmed.slice(idx + yearDelimiter.length).split(dateDelimiter);
      if (rest.length !== 2) return null;
      return [trimmed.slice(0, idx), rest[0], rest[1]];
    }
    const idx = trimmed.lastIndexOf(yearDelimiter);
    if (idx === -1) return null;
    const rest = trimmed.slice(0, idx).split(dateDelimiter);
    if (rest.length !== 2) return null;
    return [rest[0], rest[1], trimmed.slice(idx + yearDelimiter.length)];
  }
  const parts = trimmed.split(dateDelimiter);
  if (parts.length !== 3) return null;
  return [parts[0], parts[1], parts[2]];
}

function parseImportDate(text: string, options?: ImportValueConversionOptions): string | null {
  const order = options?.dateOrder ?? "ymd";
  const delim = options?.dateDelimiter && options.dateDelimiter.length > 0 ? options.dateDelimiter : "-";
  const yearDelim = options?.yearDelimiter && options.yearDelimiter.length > 0 ? options.yearDelimiter : undefined;

  const parts = splitDateParts(text, order, delim, yearDelim);
  if (!parts) return null;

  let yearText: string;
  let monthText: string;
  let dayText: string;
  if (order === "dmy") {
    [dayText, monthText, yearText] = parts;
  } else if (order === "mdy") {
    [monthText, dayText, yearText] = parts;
  } else {
    [yearText, monthText, dayText] = parts;
  }

  const y = expandTwoDigitYear(yearText);
  const m = parseMonth(monthText);
  const dTrim = dayText.trim();
  const d = /^\d+$/.test(dTrim) ? Number(dTrim) : NaN;
  if (y == null || m == null || !Number.isInteger(d) || d < 1 || d > 31) return null;
  return `${pad(y, 4)}-${pad(m, 2)}-${pad(d, 2)}`;
}

function parseImportTime(text: string, options?: ImportValueConversionOptions): string | null {
  const delim = options?.timeDelimiter && options.timeDelimiter.length > 0 ? options.timeDelimiter : ":";
  const trimmed = text.trim();
  if (!trimmed) return null;
  const parts = trimmed.split(delim);
  if (parts.length < 2 || parts.length > 3) return null;
  const h = /^\d+$/.test(parts[0]) ? Number(parts[0]) : NaN;
  const m = /^\d+$/.test(parts[1]) ? Number(parts[1]) : NaN;
  const sRaw = parts[2] ?? "0";
  const s = /^\d+$/.test(sRaw) ? Number(sRaw) : NaN;
  if (
    !Number.isInteger(h) ||
    !Number.isInteger(m) ||
    !Number.isInteger(s) ||
    h < 0 ||
    h > 23 ||
    m < 0 ||
    m > 59 ||
    s < 0 ||
    s > 59
  ) {
    return null;
  }
  return `${pad(h, 2)}:${pad(m, 2)}:${pad(s, 2)}`;
}

function parseImportDateTime(text: string, options?: ImportValueConversionOptions): string | null {
  const trimmed = text.trim();
  if (!trimmed) return null;
  const idx = trimmed.search(/\s/);
  if (idx === -1) {
    const date = parseImportDate(trimmed, options);
    return date ? `${date} 00:00:00` : null;
  }
  const left = trimmed.slice(0, idx);
  const right = trimmed.slice(idx + 1).trim();
  const order = options?.dateTimeOrder ?? "date-time";
  const datePart = order === "time-date" ? right : left;
  const timePart = order === "time-date" ? left : right;
  const date = parseImportDate(datePart, options);
  if (!date) return null;
  const time = parseImportTime(timePart, options);
  if (!time) return null;
  return `${date} ${time}`;
}

function bytesToHex(binary: string): string {
  let hex = "";
  for (let i = 0; i < binary.length; i++) {
    hex += binary.charCodeAt(i).toString(16).padStart(2, "0");
  }
  return hex;
}

// binaryHex 把源文件里按 encoding 编码的二进制解码成字节的十六进制；不是合法编码时返回 null。
function binaryHex(text: string, encoding: ImportBinaryEncoding): string | null {
  const cleaned = text.replace(/\s+/g, "");
  if (encoding === "base64") {
    if (!cleaned) return "";
    if (!/^[A-Za-z0-9+/=]+$/.test(cleaned)) return null;
    try {
      return bytesToHex(atob(cleaned));
    } catch {
      return null;
    }
  }
  if (!/^[0-9a-fA-F]*$/.test(cleaned)) return null;
  const hex = cleaned.toLowerCase();
  return hex.length % 2 === 0 ? hex : `0${hex}`;
}

function convertImportValue(
  cell: string,
  category: ImportColumnCategory | undefined,
  nullStrategy: ImportNullStrategy,
  conversion: ImportValueConversionOptions | undefined
): ImportValue {
  const value = importValue(cell, nullStrategy);
  if (value == null) return { kind: "null" };
  const text = String(value);

  if (category === "binary" && conversion?.binaryEncoding) {
    const hex = binaryHex(text, conversion.binaryEncoding);
    if (hex != null) return { kind: "binary", hex };
  } else if (category === "date") {
    const iso = parseImportDate(text, conversion);
    if (iso) return { kind: "text", text: iso };
  } else if (category === "datetime") {
    const iso = parseImportDateTime(text, conversion);
    if (iso) return { kind: "text", text: iso };
  } else if (category === "number" && conversion?.decimalSymbol && conversion.decimalSymbol !== ".") {
    return { kind: "text", text: text.split(conversion.decimalSymbol).join(".") };
  }
  return { kind: "text", text };
}

/**
 * 把映射好的源文件行整理成发给驱动的导入请求：只带映射了的列（按源文件的列序），值按目标列的类别
 * 转换（日期、小数点、二进制编码、空值策略）。各模式怎么写、字面量与自增列由驱动决定。
 */
export function buildImportIntent({
  headers,
  rows,
  mapping,
  nullStrategy,
  mode = "append",
  keys = [],
  advancedOptions,
  columnCategories = {},
  conversionOptions,
}: BuildImportIntentArgs): ImportIntent {
  const effectiveNullStrategy = advancedOptions?.emptyStringAsNull ? "empty-is-null" : nullStrategy;
  const mapped = headers
    .map((source, index) => ({ index, target: mapping[source] }))
    .filter((item): item is { index: number; target: string } => !!item.target);

  return {
    mode,
    columns: mapped.map((item) => item.target),
    keys,
    rows: rows.map((row) =>
      mapped.map((item) =>
        convertImportValue(
          row[item.index] ?? "",
          columnCategories[item.target],
          effectiveNullStrategy,
          conversionOptions
        )
      )
    ),
    extendedInsert: !!advancedOptions?.extendedInsert,
    maxStatementSizeKb: advancedOptions?.maxStatementSizeKb ?? 1024,
  };
}
