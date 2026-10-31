import type { TFunction } from "i18next";
import type { DraftIssue } from "./draft";
import { declaredLabel } from "./model";

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
    case "emptyStringIsNull":
      return t("query.designer.issue.emptyStringIsNull");
    case "indexNameEmpty":
      return t("query.designer.issue.indexNameEmpty");
    case "indexNameDuplicate":
      return t("query.designer.issue.indexNameDuplicate", { row: issue.duplicateOf });
    case "indexNameTaken":
      return t("query.designer.issue.indexNameTaken");
    case "indexColumnsEmpty":
      return t("query.designer.issue.indexColumnsEmpty");
    case "primaryKeyDuplicate":
      return t("query.designer.issue.primaryKeyDuplicate");
    case "indexOptionExclusive":
      return t("query.designer.issue.indexOptionExclusive", { option: declaredLabel(t, issue.option!) });
  }
}

/** 问题在哪里:表名、第几列、第几个索引,或整张表(没有位置)。 */
export function issueLocation(t: TFunction, issue: DraftIssue): string {
  switch (issue.target.kind) {
    case "tableName":
      return t("query.designer.issues.tableName");
    case "column":
      return t("query.designer.issues.column", { row: issue.row, name: issue.name });
    case "index":
      return t("query.designer.issues.index", { row: issue.row, name: issue.name });
    case "columns":
      return "";
  }
}
