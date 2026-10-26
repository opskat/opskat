import type { ConfigGroupSchema, FieldDesc } from "@/components/asset/configFields";
import type { DatabaseFormState } from "@/components/asset/DatabaseConfigSection.config";
import type { DriverDescriptor } from "./descriptors";
import { driverFieldsForTab, driverFieldsOf } from "./driverFields";

type DriverFieldDescs = FieldDesc<DatabaseFormState>[];

const registry = new Map<string, DriverFieldDescs>();

/**
 * 注册驱动描述里一个表单字段(dbdriver.FormField 的取值)对应的控件。
 * 驱动在某个 Tab 里声明了该字段,表单就渲染这里登记的控件;共享代码不认识任何驱动名。
 */
export function registerDriverFormField(field: string, descs: DriverFieldDescs): void {
  registry.set(field, descs);
}

const TAB_LABELS: Record<string, string> = {
  connection: "asset.tabConnection",
  tunnel: "asset.tabTunnel",
  tls: "asset.tabTls",
  advanced: "asset.tabAdvanced",
};

/**
 * 按驱动描述生成表单各 Tab 的声明式分组(Tab 与字段顺序都以描述为准)。
 * connectionLead 排在连接 Tab 最前(驱动下拉)。端口与主机在同一 Tab 里同行显示,由主机字段的控件携带。
 * 驱动专属字段排在所在 Tab 的内置字段之后;表单没声明的 Tab 由专属字段带出,按 Tab 的固定次序排在后面。
 */
export function driverFormGroups(
  driver: DriverDescriptor,
  connectionLead: DriverFieldDescs
): ConfigGroupSchema<DatabaseFormState>[] {
  const formTabs = driver.form.map((tab) => tab.tab);
  const extraTabs = Object.keys(TAB_LABELS).filter(
    (tab) => !formTabs.includes(tab) && driverFieldsOf(driver).some((f) => f.tab === tab)
  );
  const builtin = (tab: string) => {
    const fields = driver.form.find((t) => t.tab === tab)?.fields ?? [];
    return fields.filter((f) => !(f === "port" && fields.includes("host"))).flatMap((f) => registry.get(f) ?? []);
  };
  return [...formTabs, ...extraTabs].map((tab) => {
    const fields = [...builtin(tab), ...driverFieldsForTab(driver, tab)];
    return {
      key: tab,
      label: TAB_LABELS[tab],
      fields: tab === "connection" ? [...connectionLead, ...fields] : fields,
    };
  });
}
