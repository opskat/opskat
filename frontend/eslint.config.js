import js from "@eslint/js";
import tseslint from "typescript-eslint";
import reactHooks from "eslint-plugin-react-hooks";
import reactRefresh from "eslint-plugin-react-refresh";
import reactX from "eslint-plugin-react-x";
import prettier from "eslint-plugin-prettier/recommended";

// React 19 迁移完成后由 lint 挡住旧 API 回流：ref 作 prop、<Context> 简写、use(Context)。
const react19Restrictions = [
  {
    selector: "ImportSpecifier[imported.name='MutableRefObject']",
    message: "React 19：改用 RefObject<T | null>。",
  },
  {
    selector: "TSQualifiedName[right.name='MutableRefObject']",
    message: "React 19：改用 RefObject<T | null>。",
  },
];

// DESIGN.md「Use tokens, not literal colors」：组件只用语义 token，不写 Tailwind palette 类名。
const paletteColors =
  "red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose|slate|gray|zinc|neutral|stone";
const paletteClassPattern = `(^|[^-\\w])(text|bg|border|ring|fill|stroke)-(${paletteColors})-[0-9]{2,3}([^-\\w]|$)`;
const paletteMessage =
  "不写 palette 类名，用语义 token（text-warning / text-destructive / bg-background…，定义在 src/styles/globals.css）——一个颜色一处定义，明暗主题才都成立（docs/DESIGN.md → Use tokens, not literal colors）。";
const paletteRestrictions = [
  { selector: `Literal[value=/${paletteClassPattern}/]`, message: paletteMessage },
  { selector: `TemplateElement[value.raw=/${paletteClassPattern}/]`, message: paletteMessage },
];

// 数据库驱动差异在后端驱动注册表里，前端从驱动描述读取（src/lib/dbDrivers/，docs/references/adding-a-database-driver.md）：
// 共享代码不得拿驱动名做比较。驱动名取自内置驱动的 id 与别名。
const driverNames = "mysql|postgresql|postgres|mssql|sqlserver|sqlite|sqlite3";
const driverMessage =
  "共享代码不得按驱动名分支：驱动属性从 src/lib/dbDrivers 的驱动描述读取（driverHasCapability 等），方言 SQL 放进后端驱动（docs/references/adding-a-database-driver.md）。";
const driverRestrictions = [
  { selector: `BinaryExpression[operator=/^[!=]==?$/] > Literal[value=/^(${driverNames})$/]`, message: driverMessage },
  { selector: `SwitchCase[test.value=/^(${driverNames})$/]`, message: driverMessage },
];
// 写路径本轮仍由前端生成方言 SQL，下列文件因此还按驱动名分支（规格：写路径改由驱动生成是第二轮）。
// 只减不增：迁到后端驱动的文件从这里删掉；新代码读驱动描述，不进这个名单。
const driverBranchExempt = [
  "src/components/query/AlterTableDialog.tsx", // 改表：各库 ALTER 语法
  "src/components/query/CreateDatabaseDialog.tsx", // 建库：字符集 / 排序规则默认值与 CREATE DATABASE 语法
  "src/components/query/DatabaseTree.tsx", // 清空表：SQLite 没有 TRUNCATE，改发 DELETE FROM
  "src/components/query/ImportTableDataDialog.tsx", // 导入：表名限定形式与外键检查开关
  "src/lib/tableImport.ts", // 导入：二进制字面量与 upsert 写法
  "src/lib/tableSql.ts", // 引号规则与建表 / 改表 / 删表 / 清空的方言 SQL
];

export default tseslint.config(
  { ignores: ["dist", "wailsjs"] },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    plugins: {
      "react-hooks": reactHooks,
      "react-refresh": reactRefresh,
      "react-x": reactX,
    },
    rules: {
      ...reactHooks.configs.recommended.rules,
      "react-refresh/only-export-components": ["error", { allowConstantExport: true }],
      "@typescript-eslint/no-unused-vars": ["error", { argsIgnorePattern: "^_", varsIgnorePattern: "^_" }],
      "react-x/no-forward-ref": "error",
      "react-x/no-context-provider": "error",
      "react-x/no-use-context": "error",
      "no-restricted-syntax": ["error", ...react19Restrictions],
      "react-hooks/exhaustive-deps": "error",
      "react-hooks/incompatible-library": "error",
      // 成功提示统一走 notify helper（top-center）：底部 toast 会遮挡终端/AI/查询视图的输出（#135）。
      "no-restricted-properties": [
        "error",
        {
          object: "toast",
          property: "success",
          message:
            "用 src/lib/notify.ts 的 notifySuccess/notifyCopied（top-center）——底部成功 toast 会遮挡输出（#135，见 AGENTS.md）。",
        },
      ],
    },
  },
  {
    // 设计系统作用域（主应用 src + @opskat/ui）：palette 类名禁令只在 globals.css token 体系内生效。
    files: ["src/**/*.{ts,tsx}", "packages/ui/src/**/*.{ts,tsx}"],
    rules: {
      "no-restricted-syntax": ["error", ...react19Restrictions, ...paletteRestrictions, ...driverRestrictions],
    },
  },
  {
    // 驱动描述的实现处、测试夹具与写路径豁免名单：可以按驱动名比较（其余禁令保留）。
    files: [
      "src/lib/dbDrivers/**/*.{ts,tsx}",
      "src/**/__tests__/**/*.{ts,tsx}",
      "src/**/*.test.{ts,tsx}",
      ...driverBranchExempt,
    ],
    rules: {
      "no-restricted-syntax": ["error", ...react19Restrictions, ...paletteRestrictions],
    },
  },
  {
    // 守护测试自身必须包含违规 fixture 字符串，palette 禁令对它豁免（React 19 禁令保留）。
    files: ["src/__tests__/eslint-harness.test.ts"],
    rules: {
      "no-restricted-syntax": ["error", ...react19Restrictions],
    },
  },
  {
    // notify.ts 是 toast.success 的唯一封装点；其自身测试需要断言底层调用。
    files: ["src/lib/notify.ts", "src/__tests__/notify.test.ts"],
    rules: { "no-restricted-properties": "off" },
  },
  prettier
);
