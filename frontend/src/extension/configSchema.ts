// frontend/src/extension/configSchema.ts
//
// manifest configSchema 的前端读法。后端 pkg/extension/config_schema.go 有同一份契约的
// Go 侧读法（属性名 / 必填项 / format:"password" 字段），两侧都只读 manifest，不各自
// 引入额外约定。

export interface ExtensionConfigProperty {
  type?: string;
  format?: string;
  enum?: string[];
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

/** 返回 format:"password" 的属性名——它们保存前要经后端加密，展示时要打码。 */
export function passwordFields(schema?: ExtensionConfigSchema): string[] {
  const props = schema?.properties ?? {};
  return Object.entries(props)
    .filter(([, prop]) => prop.format === "password")
    .map(([name]) => name);
}
