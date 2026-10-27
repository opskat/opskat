import type { TFunction } from "i18next";
import type { DraftIssue } from "./draft";

/** 一条问题的说明(不含位置)。 */
export function issueMessage(t: TFunction, issue: DraftIssue): string {
  switch (issue.code) {
    case "tableNameEmpty":
      return t("query.designer.issue.tableNameEmpty");
    case "columnNameEmpty":
      return t("query.designer.issue.columnNameEmpty");
    case "columnNameDuplicate":
      return t("query.designer.issue.columnNameDuplicate", { row: issue.duplicateOf });
    case "columnTypeEmpty":
      return t("query.designer.issue.columnTypeEmpty");
    case "noColumns":
      return t("query.designer.issue.noColumns");
    case "nameFolding":
      return issue.folding === "upper"
        ? t("query.designer.issue.nameFolding.upper", { name: issue.name })
        : t("query.designer.issue.nameFolding.lower", { name: issue.name });
  }
}

/** 问题在哪里:表名、第几列,或整张表(没有位置)。 */
export function issueLocation(t: TFunction, issue: DraftIssue): string {
  switch (issue.target.kind) {
    case "tableName":
      return t("query.designer.issues.tableName");
    case "column":
      return t("query.designer.issues.column", { row: issue.row, name: issue.name });
    case "columns":
      return "";
  }
}
