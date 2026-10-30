import type { DesignerSubTab } from "./DesignerFrame";
import type { DesignerSave } from "./TableDesignerEditor";
import type { SaveFlowState, SavePlan } from "./useSaveFlow";

/** 关闭拦截里点「保存…」时挂着的关闭:保存全部成功后照此关闭。 */
export interface CloseAfterSave {
  ids: string[];
  closeOuter: boolean;
}

/** 设计表的一次保存:编辑器交来的内容,以及保存成功后要不要接着关闭标签。 */
export interface DesignerSavePlan extends SavePlan {
  save: DesignerSave;
  close?: CloseAfterSave;
}

/**
 * 逐条执行停在中途、但改表名已经生效时,标签要换成新名字(标签 id 随表名,设计器随之重新挂载)。
 * 结果视图与要搬到新结构上的草稿经这里交给新名字的设计器。
 */
export interface DesignerHandoff {
  flow: Extract<SaveFlowState<DesignerSavePlan>, { phase: "partial" }>;
  subTab: DesignerSubTab;
}

const handoffs = new Map<string, DesignerHandoff>();

const keyOf = (tabId: string, innerTabId: string) => `${tabId}\n${innerTabId}`;

export function handOver(tabId: string, innerTabId: string, handoff: DesignerHandoff) {
  handoffs.set(keyOf(tabId, innerTabId), handoff);
}

/** 新挂载的设计器读交给它的内容(没有时为 undefined);读完由 dropHandoff 删除(严格模式下初始化会跑两次)。 */
export function peekHandoff(tabId: string, innerTabId: string): DesignerHandoff | undefined {
  return handoffs.get(keyOf(tabId, innerTabId));
}

export function dropHandoff(tabId: string, innerTabId: string) {
  handoffs.delete(keyOf(tabId, innerTabId));
}
