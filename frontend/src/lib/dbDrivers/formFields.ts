import type { ConfigGroupSchema, FieldDesc } from "@/components/asset/configFields";
import type { DatabaseFormState } from "@/components/asset/DatabaseConfigSection.config";
import type { DriverDescriptor } from "./descriptors";

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
 */
export function driverFormGroups(
  driver: DriverDescriptor,
  connectionLead: DriverFieldDescs
): ConfigGroupSchema<DatabaseFormState>[] {
  return driver.form.map((tab) => {
    const fields = tab.fields
      .filter((f) => !(f === "port" && tab.fields.includes("host")))
      .flatMap((f) => registry.get(f) ?? []);
    return {
      key: tab.tab,
      label: TAB_LABELS[tab.tab],
      fields: tab.tab === "connection" ? [...connectionLead, ...fields] : fields,
    };
  });
}
