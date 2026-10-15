import { describe, expect, it } from "vitest";
import fs from "node:fs";
import path from "node:path";

import zhCommon from "@/i18n/locales/zh-CN/common.json";
import enCommon from "@/i18n/locales/en/common.json";
import { getAllAssetTypes } from "@/lib/assetTypes";

type LocaleTree = Record<string, unknown>;

function flattenKeys(value: unknown, prefix = ""): string[] {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return prefix ? [prefix] : [];
  }

  return Object.entries(value as LocaleTree).flatMap(([key, child]) => {
    const nextPrefix = prefix ? `${prefix}.${key}` : key;
    return flattenKeys(child, nextPrefix);
  });
}

function hasLocaleKey(locale: LocaleTree, key: string): boolean {
  return key.split(".").every((part, index, parts) => {
    const parent = parts.slice(0, index).reduce<unknown>((node, segment) => {
      return node && typeof node === "object" ? (node as LocaleTree)[segment] : undefined;
    }, locale);
    return Boolean(parent && typeof parent === "object" && part in (parent as LocaleTree));
  });
}

function walkSourceFiles(dir: string, out: string[] = []): string[] {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    if (entry.name === "__tests__") continue;

    const fullPath = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      walkSourceFiles(fullPath, out);
    } else if (/\.tsx?$/.test(entry.name)) {
      out.push(fullPath);
    }
  }
  return out;
}

function collectStaticI18nKeys(): string[] {
  const sourceRoot = path.resolve(process.cwd(), "src");
  const keys = new Set<string>();
  const patterns = [
    /(?:^|[^\w.])t\(\s*(["'`])([^"'`$]+)\1/g,
    /i18n\.t\(\s*(["'`])([^"'`$]+)\1/g,
    /i18nKey\s*=\s*(["'`])([^"'`$]+)\1/g,
  ];

  for (const file of walkSourceFiles(sourceRoot)) {
    const source = fs.readFileSync(file, "utf8");
    for (const pattern of patterns) {
      for (const match of source.matchAll(pattern)) {
        keys.add(match[2]);
      }
    }
  }

  return [...keys].sort();
}

function collectBuiltinPolicyGroupIds(): string[] {
  const policyFile = path.resolve(process.cwd(), "../internal/model/entity/policy/policy.go");
  const source = fs.readFileSync(policyFile, "utf8");
  return [...source.matchAll(/Builtin[A-Za-z0-9_]+\s+=\s+"builtin:([^"]+)"/g)].map((match) => match[1]).sort();
}

// JSON.parse 对同一对象里的重复键静默取最后一个：后加的同名键会覆盖已有译文，各处调用都会显示错的文案。
function findDuplicateKeys(source: string): string[] {
  const duplicates: string[] = [];
  const stack: { keys: Set<string>; path: string; expectKey: boolean; lastKey: string; array: boolean }[] = [];
  let i = 0;
  const readString = () => {
    let out = "";
    for (i++; source[i] !== '"'; i++) {
      if (source[i] === "\\") i++;
      out += source[i];
    }
    i++;
    return out;
  };
  while (i < source.length) {
    const c = source[i];
    const top = stack.at(-1);
    if (c === '"') {
      const text = readString();
      if (top?.expectKey) {
        if (top.keys.has(text)) duplicates.push(top.path ? `${top.path}.${text}` : text);
        top.keys.add(text);
        top.lastKey = text;
        top.expectKey = false;
      }
      continue;
    }
    if (c === "{" || c === "[") {
      const path = top ? (top.path ? `${top.path}.${top.lastKey}` : top.lastKey) : "";
      stack.push({ keys: new Set(), path, expectKey: c === "{", lastKey: "", array: c === "[" });
    } else if (c === "}" || c === "]") {
      stack.pop();
    } else if (c === "," && top && !top.array) {
      top.expectKey = true;
    }
    i++;
  }
  return duplicates;
}

describe("i18n resources", () => {
  it("keeps zh-CN and en common keys aligned", () => {
    const zhKeys = flattenKeys(zhCommon).sort();
    const enKeys = flattenKeys(enCommon).sort();

    expect(zhKeys.filter((key) => !enKeys.includes(key))).toEqual([]);
    expect(enKeys.filter((key) => !zhKeys.includes(key))).toEqual([]);
  });

  it("has no duplicate keys that silently override an existing translation", () => {
    for (const locale of ["zh-CN", "en"]) {
      const file = path.resolve(process.cwd(), `src/i18n/locales/${locale}/common.json`);
      expect({ locale, duplicates: findDuplicateKeys(fs.readFileSync(file, "utf8")) }).toEqual({
        locale,
        duplicates: [],
      });
    }
  });

  it("covers static common translation calls", () => {
    const zhKeys = new Set(flattenKeys(zhCommon));
    const enKeys = new Set(flattenKeys(enCommon));
    const keys = collectStaticI18nKeys().filter((key) => !key.includes(":"));

    expect(keys.filter((key) => !zhKeys.has(key))).toEqual([]);
    expect(keys.filter((key) => !enKeys.has(key))).toEqual([]);
  });

  it("covers every built-in policy group label and description", () => {
    const missing: string[] = [];

    for (const id of collectBuiltinPolicyGroupIds()) {
      for (const suffix of ["name", "desc"]) {
        const key = `asset.policyGroup.builtin.${id}.${suffix}`;
        if (!hasLocaleKey(zhCommon, key) || !hasLocaleKey(enCommon, key)) {
          missing.push(key);
        }
      }
    }

    expect(missing).toEqual([]);
  });

  it("covers built-in asset type policy keys", () => {
    // 只有内置类型的文案住在 common.json 里；扩展提供的类型带自己的 `ext-<name>` 命名空间，
    // 而它们在这个测试进程里根本没注册（注册表此刻只有内置类型）。
    const keys = getAllAssetTypes().flatMap((def) => {
      if (!def.policy) return [];
      return [
        def.policy.titleKey,
        def.policy.hintKey,
        def.policy.testPlaceholderKey,
        ...def.policy.fields.flatMap((field) => [field.labelKey, field.placeholderKey]),
      ].filter((key): key is string => !!key);
    });

    expect(keys.filter((key) => !hasLocaleKey(zhCommon, key))).toEqual([]);
    expect(keys.filter((key) => !hasLocaleKey(enCommon, key))).toEqual([]);
  });
});
