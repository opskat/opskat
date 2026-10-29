import { memo, useCallback, useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { ChevronDown, Database, FolderTree } from "lucide-react";
import {
  Button,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@opskat/ui";
import { designerTabId, useQueryStore } from "@/stores/queryStore";
import { useTabStore, type QueryTabMeta } from "@/stores/tabStore";
import { notifySuccess } from "@/lib/notify";
import { driverHasCapability, findDatabaseDriver, refreshDatabaseDrivers, useDatabaseDrivers } from "@/lib/dbDrivers";
import { useCanCreateTable } from "./access";
import { createStructure, presetDraft, qualifiedTableName } from "./createDraft";
import { knownSchemas, schemaOfTable, targetLabel } from "./createTarget";
import { DesignerLoadError, DesignerSkeleton } from "./DesignerStates";
import type { DesignerSubTab } from "./DesignerFrame";
import { TableDesignerEditor, type CreateMode, type DesignerSave } from "./TableDesignerEditor";
import { useSaveFlow, type SaveOutcome } from "./useSaveFlow";
import { SaveDialog } from "./SaveDialog";
import { handOver, type CloseAfterSave, type DesignerSavePlan } from "./handoff";

export interface TableCreatorProps {
  tabId: string;
  innerTabId: string;
  /** 目标库。 */
  database: string;
  /** 有 schema 的驱动的目标 schema;空表示驱动的默认 schema。 */
  schema?: string;
}

/**
 * 新建表的内层标签:与设计表同一个编辑器,从预置的 id 列开始;工具栏表名前选目标库(有 schema 的驱动再选
 * schema),预览与执行的是驱动写出的建表语句。建好后标签转为这张表的设计标签;逐条执行停在中途而表已建出时
 * 同样转过去,结果视图与草稿交给设计标签(没执行的改动留在草稿里);表没建出时草稿留在这里。
 */
export const TableCreator = memo(function TableCreator({
  tabId,
  innerTabId,
  database,
  schema = "",
}: TableCreatorProps) {
  const { t } = useTranslation();
  const meta = useTabStore((s) => s.tabs.find((tab) => tab.id === tabId)?.meta as QueryTabMeta | undefined);
  const isOuterActive = useTabStore((s) => s.activeTabId === tabId);
  const isInnerActive = useQueryStore((s) => s.dbStates[tabId]?.activeInnerTabId === innerTabId);
  const databases = useQueryStore((s) => s.dbStates[tabId]?.databases);
  const tables = useQueryStore((s) => s.dbStates[tabId]?.tables[database]);
  const refreshTables = useQueryStore((s) => s.refreshTables);
  const retargetCreateTab = useQueryStore((s) => s.retargetCreateTab);
  const completeCreateTab = useQueryStore((s) => s.completeCreateTab);
  const forceCloseInnerTabs = useQueryStore((s) => s.forceCloseInnerTabs);
  const closeInnerTab = useQueryStore((s) => s.closeInnerTab);
  const assetId = meta?.assetId ?? 0;

  const driversState = useDatabaseDrivers();
  const descriptor =
    driversState.status === "ready" ? findDatabaseDriver(driversState.drivers, meta?.driver ?? "") : undefined;
  const canCreate = useCanCreateTable(descriptor, assetId);
  const schemasDeclared = driverHasCapability(descriptor, "schemas");
  // 驱动描述重新取过(扩展加载完、换语言)后是新对象:「读出的表」只随索引能不能改而变,声明没变时草稿不重来。
  const indexesEditable = !!descriptor?.indexEditing;
  const structure = useMemo(() => createStructure(indexesEditable), [indexesEditable]);
  const preset = useMemo(() => presetDraft(descriptor), [descriptor]);
  const qualify = useCallback((name: string) => qualifiedTableName(schema, name), [schema]);
  const [subTab, setSubTab] = useState<DesignerSubTab>("fields");
  // 执行完到标签转过去(或确定留下)之间还在刷新树:这段时间同样算保存中,不能再建一次。
  const [settling, setSettling] = useState(false);

  /**
   * 建出的表在刷新后的树上叫什么:选了 schema(或驱动没有 schema)时就是请求里的表名;用默认 schema 建的,
   * 按树上列出的 schema.表 找(只有一个同名表时)。树上没有时为 null。
   */
  const findCreated = (name: string): string | null => {
    const listed = useQueryStore.getState().dbStates[tabId]?.tables[database] ?? [];
    const qualified = qualifiedTableName(schema, name);
    if (listed.includes(qualified)) return qualified;
    if (!schemasDeclared || schema) return null;
    const matches = listed.filter((table) => {
      const prefix = schemaOfTable(table);
      return prefix !== "" && table.slice(prefix.length + 1) === name;
    });
    return matches.length === 1 ? matches[0] : null;
  };

  const afterSave = async (plan: DesignerSavePlan, outcome: SaveOutcome<DesignerSavePlan>) => {
    const name = plan.save.draft.name.trim();
    if (outcome.kind === "success") {
      notifySuccess(t("query.designer.create.done", { name: `${database}.${plan.save.table}` }));
      // 连外层查询标签一起关时树也不在了,不必刷新。
      if (plan.close?.closeOuter) {
        forceCloseInnerTabs(tabId, plan.close.ids, true);
        return;
      }
      await refreshTables(tabId, database);
      if (plan.close) forceCloseInnerTabs(tabId, plan.close.ids);
      else completeCreateTab(tabId, innerTabId, findCreated(name) ?? plan.save.table);
      return;
    }
    // 停在中途:表建出来了就转为它的设计标签,结果视图与草稿交过去;没建出来时留在这里,草稿不动。
    await refreshTables(tabId, database);
    const created = findCreated(name);
    if (!created) return;
    handOver(tabId, designerTabId(database, created), { flow: outcome.state, subTab });
    completeCreateTab(tabId, innerTabId, created);
  };
  const flow = useSaveFlow<DesignerSavePlan>({
    assetId,
    database,
    onExecuted: (plan, outcome) => {
      setSettling(true);
      void afterSave(plan, outcome).finally(() => setSettling(false));
    },
  });
  const startSave = (save: DesignerSave, close?: CloseAfterSave) =>
    flow.open({
      title: t("query.designer.create.confirmTitle", { name: `${database}.${save.table}` }),
      statements: save.statements,
      droppedColumns: [],
      droppedIndexes: [],
      save,
      close,
    });

  if (driversState.status === "error") {
    return (
      <DesignerLoadError
        error={driversState.error}
        onRetry={refreshDatabaseDrivers}
        onClose={() => closeInnerTab(tabId, innerTabId)}
      />
    );
  }
  if (driversState.status === "loading") return <DesignerSkeleton />;
  if (!canCreate) {
    // 入口在不能建表时不出现;这里只会是恢复出来的标签(如之后开启了只读连接)。
    return (
      <DesignerLoadError
        error={t("query.designer.create.unavailable")}
        onRetry={refreshDatabaseDrivers}
        onClose={() => closeInnerTab(tabId, innerTabId)}
      />
    );
  }

  const databaseChoices = databases?.includes(database) ? databases : [database, ...(databases ?? [])];
  const schemaChoices = knownSchemas(tables);
  if (schema && !schemaChoices.includes(schema)) schemaChoices.unshift(schema);
  const target = (
    <>
      <span className="shrink-0 text-[11px] text-muted-foreground">{t("query.designer.create.target")}</span>
      <TargetPicker
        testId="designer-create-database"
        title={t("query.designer.create.database")}
        icon={<Database className="h-3 w-3 text-muted-foreground" aria-hidden />}
        text={database}
        value={database}
        choices={databaseChoices.map((db) => ({ value: db, label: db }))}
        onChange={(db) => retargetCreateTab(tabId, innerTabId, db)}
      />
      {schemasDeclared && (
        <TargetPicker
          testId="designer-create-schema"
          title={t("query.designer.create.schema")}
          icon={<FolderTree className="h-3 w-3 text-muted-foreground" aria-hidden />}
          text={schema || t("query.designer.create.defaultSchema")}
          value={schema}
          choices={[
            { value: "", label: t("query.designer.create.defaultSchema") },
            ...schemaChoices.map((s) => ({ value: s, label: s })),
          ]}
          onChange={(next) => retargetCreateTab(tabId, innerTabId, database, next || undefined)}
        />
      )}
      <span className="text-muted-foreground" aria-hidden>
        .
      </span>
    </>
  );
  const create: CreateMode = {
    target,
    qualify,
    label: t("query.designer.create.label", { name: targetLabel({ database, schema }) }),
    targetName: targetLabel({ database, schema }),
    preset,
  };

  return (
    <>
      <TableDesignerEditor
        tabId={tabId}
        innerTabId={innerTabId}
        active={isOuterActive && isInnerActive}
        assetId={assetId}
        database={database}
        table=""
        descriptor={descriptor}
        structure={structure}
        subTab={subTab}
        onSubTabChange={setSubTab}
        onReload={() => undefined}
        rebaseFrom={null}
        saving={settling || flow.state.phase !== "idle"}
        onSave={startSave}
        create={create}
      />
      <SaveDialog
        state={flow.state}
        transactional={!!descriptor?.transactionalDDL}
        // 表建出来时结果视图随标签交给设计标签(由它重新读取);留在这里的是没建出来的,草稿没动。
        partialNote={t("query.designer.create.partialKept")}
        onConfirm={() => void flow.confirm()}
        onClose={flow.close}
      />
    </>
  );
});

interface TargetPickerProps {
  testId: string;
  title: string;
  icon: ReactNode;
  text: string;
  value: string;
  choices: { value: string; label: string }[];
  onChange: (value: string) => void;
}

/** 工具栏上的目标库 / schema 选择:按钮显示当前值,下拉单选。 */
function TargetPicker({ testId, title, icon, text, value, choices, onChange }: TargetPickerProps) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="outline"
          size="xs"
          data-testid={testId}
          title={title}
          className="h-7 max-w-40 shrink-0 gap-1 px-2 font-mono text-xs"
        >
          {icon}
          <span className="truncate">{text}</span>
          <ChevronDown className="h-3 w-3 text-muted-foreground" aria-hidden />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="max-h-72 overflow-auto">
        <DropdownMenuRadioGroup value={value} onValueChange={onChange}>
          {choices.map((choice) => (
            <DropdownMenuRadioItem key={choice.value} value={choice.value} className="font-mono text-xs">
              {choice.label}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
