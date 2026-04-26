import { quoteIdent, sqlQuote } from "./tableSql";

export type Delimiter = "," | "\t";
export type ImportNullStrategy = "empty-is-empty-string" | "empty-is-null" | "literal-null";
export type ImportDataFormat = "text" | "csv" | "json" | "xml";
export type ImportFieldDelimiter = "," | "\t" | ";" | "|" | " ";

export interface ParsedDelimitedTable {
  headers: string[];
  rows: string[][];
}

export interface ParseImportSourceTextArgs {
  text: string;
  format: ImportDataFormat;
  fieldDelimiter?: ImportFieldDelimiter;
  fixedWidth?: boolean;
  fieldNameRowEnabled?: boolean;
  fieldNameRow?: number;
  dataStartRow?: number;
  dataEndRow?: number;
}

export interface BuildImportInsertSqlArgs {
  tableName: string;
  headers: string[];
  rows: string[][];
  mapping: Record<string, string>;
  nullStrategy: ImportNullStrategy;
  driver?: string;
}

export function detectDelimiter(text: string): Delimiter {
  const firstLine = text.split(/\r?\n/, 1)[0] ?? "";
  const tabs = (firstLine.match(/\t/g) ?? []).length;
  const commas = (firstLine.match(/,/g) ?? []).length;
  return tabs > commas ? "\t" : ",";
}

function parseDelimitedRows(text: string, delimiter: ImportFieldDelimiter = detectDelimiter(text)): string[][] {
  const rows: string[][] = [];
  let currentRow: string[] = [];
  let current = "";
  let inQuotes = false;

  for (let i = 0; i < text.length; i++) {
    const ch = text[i];
    const next = text[i + 1];

    if (ch === '"') {
      if (inQuotes && next === '"') {
        current += '"';
        i++;
      } else {
        inQuotes = !inQuotes;
      }
      continue;
    }

    if (ch === delimiter && !inQuotes) {
      currentRow.push(current);
      current = "";
      continue;
    }

    if ((ch === "\n" || ch === "\r") && !inQuotes) {
      if (ch === "\r" && next === "\n") i++;
      currentRow.push(current);
      if (currentRow.some((cell) => cell !== "")) rows.push(currentRow);
      currentRow = [];
      current = "";
      continue;
    }

    current += ch;
  }

  currentRow.push(current);
  if (currentRow.some((cell) => cell !== "")) rows.push(currentRow);

  return rows;
}

export function parseDelimitedText(text: string, delimiter: Delimiter = detectDelimiter(text)): ParsedDelimitedTable {
  const rows = parseDelimitedRows(text, delimiter);
  const headers = rows[0] ?? [];
  return { headers, rows: rows.slice(1) };
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

  const rows = args.fixedWidth ? parseFixedWidthRows(args.text) : parseDelimitedRows(args.text, args.fieldDelimiter);
  return applyRowOptions(rows, args);
}

function quoteTableName(tableName: string, driver?: string): string {
  return tableName
    .split(".")
    .filter(Boolean)
    .map((part) => quoteIdent(part, driver))
    .join(".");
}

function importValue(cell: string, nullStrategy: ImportNullStrategy): unknown {
  if (nullStrategy === "empty-is-null" && cell === "") return null;
  if (nullStrategy === "literal-null" && cell.toUpperCase() === "NULL") return null;
  return cell;
}

export function buildImportInsertSql({
  tableName,
  headers,
  rows,
  mapping,
  nullStrategy,
  driver,
}: BuildImportInsertSqlArgs): string[] {
  const mapped = headers
    .map((source, index) => ({ source, index, target: mapping[source] }))
    .filter((item): item is { source: string; index: number; target: string } => !!item.target);

  if (mapped.length === 0) return [];

  const quotedTable = quoteTableName(tableName, driver);
  const columnSql = mapped.map((item) => quoteIdent(item.target, driver)).join(", ");

  return rows.map((row) => {
    const values = mapped.map((item) => sqlQuote(importValue(row[item.index] ?? "", nullStrategy))).join(", ");
    return `INSERT INTO ${quotedTable} (${columnSql}) VALUES (${values});`;
  });
}
