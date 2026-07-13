import js from "@eslint/js";
import tseslint from "typescript-eslint";
import reactHooks from "eslint-plugin-react-hooks";
import reactRefresh from "eslint-plugin-react-refresh";
import reactX from "eslint-plugin-react-x";
import prettier from "eslint-plugin-prettier/recommended";

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
      // React 19 迁移完成后由 lint 挡住旧 API 回流：ref 作 prop、<Context> 简写、use(Context)。
      "react-x/no-forward-ref": "error",
      "react-x/no-context-provider": "error",
      "react-x/no-use-context": "error",
      "no-restricted-syntax": [
        "error",
        {
          selector: "ImportSpecifier[imported.name='MutableRefObject']",
          message: "React 19：改用 RefObject<T | null>。",
        },
        {
          selector: "TSQualifiedName[right.name='MutableRefObject']",
          message: "React 19：改用 RefObject<T | null>。",
        },
      ],
      "react-hooks/exhaustive-deps": "error",
      "react-hooks/incompatible-library": "error",
    },
  },
  prettier
);
