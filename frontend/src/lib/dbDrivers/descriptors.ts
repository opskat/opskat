import { useEffect, useState, useSyncExternalStore } from "react";
// 应用的 i18n（src/i18n）初始化的就是 i18next 的默认实例；这里只读当前语言，不引入它的初始化副作用。
import i18n from "i18next";
import { ListDatabaseDrivers } from "../../../wailsjs/go/query/Query";
import type { dbdriver } from "../../../wailsjs/go/models";

/** 后端驱动注册表里一个驱动的自我描述(原样来自 Query.ListDatabaseDrivers,专属字段的文案已按当前语言翻译)。 */
export type DriverDescriptor = dbdriver.Descriptor;

let cached: Promise<DriverDescriptor[]> | null = null;
let generation = 0;
const listeners = new Set<() => void>();

/**
 * 取全部驱动描述,顺序即后端返回的展示顺序。成功结果复用到注册表或界面语言下次变化;
 * 失败不缓存,下次调用重试。
 */
export function loadDatabaseDrivers(): Promise<DriverDescriptor[]> {
  if (!cached) {
    const pending = ListDatabaseDrivers(i18n.language);
    cached = pending;
    pending.catch(() => {
      if (cached === pending) cached = null;
    });
  }
  return cached;
}

/**
 * 后端注册表变了(扩展加载完成、启用、停用、安装、升级、卸载)或界面换了语言:丢弃缓存的描述,
 * 已挂载的 useDatabaseDrivers 重新取一次。
 */
export function refreshDatabaseDrivers(): void {
  cached = null;
  generation++;
  for (const listener of listeners) listener();
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

function currentGeneration(): number {
  return generation;
}

export type DatabaseDriversState =
  { status: "loading" } | { status: "ready"; drivers: DriverDescriptor[] } | { status: "error"; error: string };

/**
 * 加载驱动描述,并在注册表变化后换成新的列表;加载失败时给出错误文案,不提供任何替代列表。
 * 重新取的过程中仍给出上一份列表,使用方(表单、查询面板)不会被卸载重来。
 */
export function useDatabaseDrivers(): DatabaseDriversState {
  const gen = useSyncExternalStore(subscribe, currentGeneration);
  const [state, setState] = useState<DatabaseDriversState>({ status: "loading" });
  useEffect(() => {
    let alive = true;
    loadDatabaseDrivers().then(
      (drivers) => alive && setState({ status: "ready", drivers }),
      (err) => alive && setState({ status: "error", error: err instanceof Error ? err.message : String(err) })
    );
    return () => {
      alive = false;
    };
  }, [gen]);
  return state;
}

/** 按驱动 ID 查描述;注册表里没有时为 undefined。 */
export function findDatabaseDriver(drivers: DriverDescriptor[], id: string): DriverDescriptor | undefined {
  return drivers.find((d) => d.id === id);
}

/** 对象浏览的四类对象。各类的名字既是驱动要声明的功能名,也是后端对象列表里的分组键。 */
export const OBJECT_BROWSER_CAPABILITIES = ["views", "procedures", "functions", "triggers"] as const;

/** 驱动是否声明了某项功能(Go 侧 dbdriver.Capability 的取值,如 "view_ddl");未声明即不支持。 */
export function driverHasCapability(driver: DriverDescriptor | undefined, capability: string): boolean {
  return !!driver && driver.capabilities.includes(capability);
}

/**
 * 改表时驱动是否能改这项属性(Go 侧 dbdriver.AlterAttribute 的取值,如 "column_type");改表名、给列改名、
 * 新增与删除列不在其列,声明了改表就能做。描述里没有这份声明(null)即一项也不能改。
 */
export function driverCanAlter(driver: DriverDescriptor | undefined, attribute: string): boolean {
  return !!driver?.alterTableAttributes?.includes(attribute);
}

/** 驱动是否在资产表单的任一 Tab 里使用该字段(Go 侧 dbdriver.FormField 的取值,如 "host")。 */
export function driverUsesField(driver: DriverDescriptor, field: string): boolean {
  return driver.form.some((tab) => tab.fields.includes(field));
}
