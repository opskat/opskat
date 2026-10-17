import type { TableFilterOperator } from "./tableFilterOperators";

export function sqlQuote(value: unknown): string {
  if (value == null) return "NULL";
  const s = String(value);
  const escaped = s.replace(/'/g, "''");
  return `'${escaped}'`;
}

export function quoteIdent(name: string, driver?: string): string {
  if (driver === "postgresql" || driver === "sqlite") return `"${name.replace(/"/g, '""')}"`;
  if (driver === "mssql") return `[${name.replace(/\]/g, "]]")}]`;
  return `\`${name.replace(/`/g, "``")}\``;
}

export function quoteQualifiedIdent(name: string, driver?: string): string {
  return name
    .split(".")
    .filter(Boolean)
    .map((part) => quoteIdent(part, driver))
    .join(".");
}

export type CellValueFilterOperator = TableFilterOperator;
