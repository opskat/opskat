// frontend/src/extension/configSchema.ts
//
// manifest configSchema 的前端读法。后端 pkg/extension/config_schema.go 有同一份契约的
// Go 侧读法（属性名 / 必填项 / format:"password" 字段），两侧都只读 manifest，不各自
// 引入额外约定。

export interface ExtensionConfigProperty {
  type?: string;
  format?: string;
  enum?: string[];
  /** 与 enum 一一对应的显示标签（后端已按扩展语言翻译）；缺省时显示原值。 */
  enumLabels?: string[];
  /** 新建资产时预选的值。 */
  default?: string;
  title?: string;
  description?: string;
  placeholder?: string;
}

export interface ExtensionConfigSchema {
  type?: string;
  properties?: Record<string, ExtensionConfigProperty>;
  required?: string[];
  propertyOrder?: string[];
}

/**
 * 表单呈现的字段及其顺序：有 propertyOrder 时按它，只取其中已声明的属性；否则按声明顺序。
 * 保存校验的逐字段错误也按它判断能否落到字段上——没呈现的字段无处显示错误。
 */
export function formFields(schema?: ExtensionConfigSchema): [string, ExtensionConfigProperty][] {
  const properties = schema?.properties ?? {};
  const order = schema?.propertyOrder;
  if (!order) return Object.entries(properties);
  return order.filter((k) => Object.prototype.hasOwnProperty.call(properties, k)).map((k) => [k, properties[k]]);
}

/** 返回 format:"password" 的属性名——它们保存前要经后端加密，展示时要打码。 */
export function passwordFields(schema?: ExtensionConfigSchema): string[] {
  const props = schema?.properties ?? {};
  return Object.entries(props)
    .filter(([, prop]) => prop.format === "password")
    .map(([name]) => name);
}

/** 声明了 default 的属性的默认值：新建资产的表单以它起步。 */
export function defaultValues(schema?: ExtensionConfigSchema): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const [name, prop] of Object.entries(schema?.properties ?? {})) {
    if (prop.default !== undefined) out[name] = prop.default;
  }
  return out;
}
