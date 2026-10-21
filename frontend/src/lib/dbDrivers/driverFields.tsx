import { ExtensionConfigForm } from "@/components/asset/ExtensionConfigForm";
import type { FieldDesc } from "@/components/asset/configFields";
import type { DatabaseFormState } from "@/components/asset/DatabaseConfigSection.config";
import type { ExtensionConfigProperty, ExtensionConfigSchema } from "@/extension/configSchema";
import type { dbdriver } from "../../../wailsjs/go/models";
import type { DriverDescriptor } from "./descriptors";

/**
 * 驱动专属字段(Descriptor.DriverFields)在前端的读法。值存在资产配置的 driver_options 里;
 * 口令类(password)保存前经后端加密,编辑时不回显,留空即沿用已存的密文。
 */
type DriverField = dbdriver.DriverField;

/** 驱动声明的专属字段;内置驱动不声明(后端给 null)。 */
export function driverFieldsOf(driver: DriverDescriptor): DriverField[] {
  return driver.driverFields ?? [];
}

const isPassword = (f: DriverField) => f.type === "password";

/** 资产上已存的、属于该驱动的 driver_options;存的是别的驱动(切换了驱动)时没有。 */
function storedOptionsOf(driver: DriverDescriptor, state: DatabaseFormState): Record<string, unknown> {
  return state.storedDriverOptions.driver === driver.id ? state.storedDriverOptions.values : {};
}

/** 已存的口令类取值(密文);表单不回显它,只据此显示「已设置」并在留空时沿用。 */
function storedSecret(driver: DriverDescriptor, state: DatabaseFormState, key: string): string {
  const v = storedOptionsOf(driver, state)[key];
  return typeof v === "string" ? v : "";
}

/**
 * 表单上各专属字段当前的取值:用户改过的取改后的值,否则取资产已存的值,再否则取描述的默认值;
 * 开关没有默认值时为关。口令类只有用户输入的明文(没输入为空串)。
 */
function driverOptionValues(driver: DriverDescriptor, state: DatabaseFormState): Record<string, unknown> {
  const edits = state.driverOptions;
  const stored = storedOptionsOf(driver, state);
  const out: Record<string, unknown> = {};
  for (const f of driverFieldsOf(driver)) {
    if (isPassword(f)) out[f.key] = edits[f.key] ?? "";
    else if (f.key in edits) out[f.key] = edits[f.key];
    else if (f.key in stored) out[f.key] = stored[f.key];
    else if (f.default !== undefined) out[f.key] = f.default;
    else if (f.type === "switch") out[f.key] = false;
  }
  return out;
}

/** 取值是否是该字段声明的类型的一个值:数字只收整数,枚举只收声明里的取值(与后端 checkValue 一致)。 */
function hasValue(f: DriverField, v: unknown): boolean {
  if (f.type === "number") return typeof v === "number" && Number.isInteger(v);
  if (f.type === "switch") return typeof v === "boolean";
  if (f.type === "enum") return typeof v === "string" && (f.enum ?? []).includes(v);
  return typeof v === "string" && v.trim() !== "";
}

/** 是否有必填的专属字段还没有值;口令类已存过也算有值。 */
export function missingRequiredDriverField(driver: DriverDescriptor, state: DatabaseFormState): boolean {
  const values = driverOptionValues(driver, state);
  return driverFieldsOf(driver).some(
    (f) => f.required && !hasValue(f, values[f.key]) && !(isPassword(f) && storedSecret(driver, state, f.key) !== "")
  );
}

/** 是否有填了却不合声明的数字类取值(如小数):保存时不能悄悄丢掉用户填的东西,先拦下。 */
export function invalidDriverField(driver: DriverDescriptor, state: DatabaseFormState): boolean {
  const values = driverOptionValues(driver, state);
  return driverFieldsOf(driver).some(
    (f) => f.type === "number" && values[f.key] != null && !hasValue(f, values[f.key])
  );
}

/**
 * 写进资产配置的 driver_options:只含该驱动声明的键,没有值的不写;口令类输入了就加密,
 * 没输入则沿用已存的密文。一个也没有时返回 undefined(配置里不出现 driver_options)。
 * 加密失败由 encrypt 的 reject 透传给调用方。
 */
export async function buildDriverOptions(
  driver: DriverDescriptor,
  state: DatabaseFormState,
  encrypt: (plain: string) => Promise<string>
): Promise<Record<string, unknown> | undefined> {
  const values = driverOptionValues(driver, state);
  const out: Record<string, unknown> = {};
  for (const f of driverFieldsOf(driver)) {
    const v = values[f.key];
    if (isPassword(f)) {
      const secret = typeof v === "string" && v !== "" ? await encrypt(v) : storedSecret(driver, state, f.key);
      if (secret) out[f.key] = secret;
    } else if (hasValue(f, v)) {
      out[f.key] = v;
    }
  }
  return Object.keys(out).length > 0 ? out : undefined;
}

/** 专属字段类型对应的扩展配置属性(决定用哪种控件)。 */
const SCHEMA_TYPES: Record<string, ExtensionConfigProperty> = {
  text: { type: "string" },
  number: { type: "integer" },
  switch: { type: "boolean" },
  enum: { type: "string" },
  password: { type: "string", format: "password" },
  textarea: { type: "string", format: "textarea" },
};

/** 把一组专属字段换成扩展配置表单的 configSchema,沿用同一套字段渲染(标签与说明已由扩展的多语言解析)。 */
function configSchemaOf(fields: DriverField[]): ExtensionConfigSchema {
  return {
    properties: Object.fromEntries(
      fields.map((f) => [f.key, { ...SCHEMA_TYPES[f.type], enum: f.enum, title: f.label, description: f.description }])
    ),
    required: fields.filter((f) => f.required).map((f) => f.key),
    propertyOrder: fields.map((f) => f.key),
  };
}

/** 驱动在某个 Tab 里声明的专属字段,作为该 Tab 的一个表单块;没有时为空。 */
export function driverFieldsForTab(driver: DriverDescriptor, tab: string): FieldDesc<DatabaseFormState>[] {
  const fields = driverFieldsOf(driver).filter((f) => f.tab === tab);
  if (fields.length === 0) return [];
  const schema = configSchemaOf(fields);
  return [
    {
      kind: "custom",
      render: (state, patch) => (
        <ExtensionConfigForm
          configSchema={schema}
          value={driverOptionValues(driver, state)}
          onChange={(next) => patch({ driverOptions: next })}
          withheldSecrets={Object.fromEntries(
            fields.filter((f) => isPassword(f) && storedSecret(driver, state, f.key) !== "").map((f) => [f.key, f.key])
          )}
        />
      ),
    },
  ];
}
