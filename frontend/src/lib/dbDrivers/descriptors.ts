import { useEffect, useState } from "react";
import { ListDatabaseDrivers } from "../../../wailsjs/go/query/Query";
import type { dbdriver } from "../../../wailsjs/go/models";

/** 后端驱动注册表里一个驱动的自我描述(原样来自 Query.ListDatabaseDrivers)。 */
export type DriverDescriptor = dbdriver.Descriptor;

let cached: Promise<DriverDescriptor[]> | null = null;

/** 取全部驱动描述,顺序即后端返回的展示顺序。成功结果在进程内复用;失败不缓存,下次调用重试。 */
export function loadDatabaseDrivers(): Promise<DriverDescriptor[]> {
  if (!cached) {
    const pending = ListDatabaseDrivers();
    cached = pending;
    pending.catch(() => {
      if (cached === pending) cached = null;
    });
  }
  return cached;
}

/** 丢弃缓存的描述列表(测试里换一份描述时用)。 */
export function resetDatabaseDriverCache(): void {
  cached = null;
}

export type DatabaseDriversState =
  { status: "loading" } | { status: "ready"; drivers: DriverDescriptor[] } | { status: "error"; error: string };

/** 加载驱动描述;加载失败时给出错误文案,不提供任何替代列表。 */
export function useDatabaseDrivers(): DatabaseDriversState {
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
  }, []);
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
