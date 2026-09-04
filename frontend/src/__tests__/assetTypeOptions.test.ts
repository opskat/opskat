import { describe, it, expect, afterEach } from "vitest";
import {
  getAssetTypeOptions,
  matchSelectedTypes,
  buildAssetTypeGroups,
  filterAssetTypeOptions,
  getAssetTypeLabel,
  resolveAssetTypeLabel,
} from "@/lib/assetTypes/options";
import { getAssetType } from "@/lib/assetTypes";
import { registerExtensionAssetTypes, unregisterExtensionAssetTypes } from "@/extension/assetTypes";
import type { ExtManifest } from "@/extension/types";
import { asset_entity } from "../../wailsjs/go/models";

const BUILTIN_VALUES = [
  "ssh",
  "database",
  "redis",
  "mongodb",
  "kafka",
  "k8s",
  "serial",
  "local",
  "vnc",
  "rdp",
  "etcd",
  "oss",
];

function manifest(name: string, assetTypes: { type: string; i18n: { name: string } }[]): ExtManifest {
  return {
    name,
    version: "1.0.0",
    icon: "Server",
    i18n: { displayName: `${name} display`, description: "" },
    assetTypes,
  } as ExtManifest;
}

// 扩展类型经真实注册路径进注册表；getAssetTypeOptions 不再收第二份清单。
const installed: string[] = [];
function install(m: ExtManifest) {
  registerExtensionAssetTypes(m.name, m);
  installed.push(m.name);
}
afterEach(() => {
  while (installed.length > 0) unregisterExtensionAssetTypes(installed.pop()!);
});

describe("getAssetTypeOptions", () => {
  it("returns built-in options when no extension is installed", () => {
    expect(getAssetTypeOptions().map((o) => o.value)).toEqual(BUILTIN_VALUES);
    expect(getAssetTypeOptions().every((o) => o.group === "builtin")).toBe(true);
  });

  it("aliases on database include mysql, postgresql, database", () => {
    const db = getAssetTypeOptions().find((o) => o.value === "database")!;
    expect(new Set(db.aliases)).toEqual(new Set(["database", "mysql", "postgresql"]));
  });

  it("includes an installed extension's asset types after the built-ins", () => {
    install(manifest("k8sExt", [{ type: "kubernetes-ext", i18n: { name: "Kubernetes" } }]));
    const opts = getAssetTypeOptions();
    expect(opts.slice(0, BUILTIN_VALUES.length).map((o) => o.value)).toEqual(BUILTIN_VALUES);
    const ext = opts.find((o) => o.value === "kubernetes-ext")!;
    expect(ext.group).toBe("extension");
    expect(ext.label).toBe("Kubernetes");
  });

  it("ignores extensions without assetTypes", () => {
    install(manifest("otherExt", []));
    expect(getAssetTypeOptions().filter((o) => o.group === "extension")).toEqual([]);
  });

  it("drops an extension's types again when it is disabled", () => {
    install(manifest("gone", [{ type: "gone-type", i18n: { name: "Gone" } }]));
    expect(getAssetTypeOptions().some((o) => o.value === "gone-type")).toBe(true);
    unregisterExtensionAssetTypes("gone");
    installed.pop();
    expect(getAssetTypeOptions().some((o) => o.value === "gone-type")).toBe(false);
  });
});

describe("built-in options derive from the registry (single source)", () => {
  it("each built-in option's icon is the same component as its registry definition's icon", () => {
    const builtins = getAssetTypeOptions().filter((o) => o.group === "builtin");
    expect(builtins.length).toBeGreaterThan(0);
    for (const opt of builtins) {
      const def = getAssetType(opt.value);
      expect(def).toBeDefined();
      expect(opt.icon).toBe(def!.icon); // 同一个组件引用，而非两处各自声明
    }
  });
});

describe("matchSelectedTypes", () => {
  const a = (id: number, type: string) => new asset_entity.Asset({ ID: id, Name: `n${id}`, Type: type });
  const assets = [a(1, "ssh"), a(2, "mysql"), a(3, "postgresql"), a(4, "redis"), a(5, "kubernetes-ext")];

  it("matches database aliases (mysql, postgresql)", () => {
    expect(matchSelectedTypes(assets, ["database"], getAssetTypeOptions()).map((x) => x.ID)).toEqual([2, 3]);
  });

  it("matches extension type", () => {
    install(manifest("k8sExt", [{ type: "kubernetes-ext", i18n: { name: "Kubernetes" } }]));
    expect(matchSelectedTypes(assets, ["kubernetes-ext"], getAssetTypeOptions()).map((x) => x.ID)).toEqual([5]);
  });

  it("treats empty selection as no filter (returns all)", () => {
    expect(matchSelectedTypes(assets, [], getAssetTypeOptions()).map((x) => x.ID)).toEqual([1, 2, 3, 4, 5]);
  });

  it("matches case-insensitively", () => {
    const assetsMixed = [a(1, "SSH"), a(2, "MySQL")];
    expect(matchSelectedTypes(assetsMixed, ["ssh"], getAssetTypeOptions()).map((x) => x.ID)).toEqual([1]);
    expect(matchSelectedTypes(assetsMixed, ["database"], getAssetTypeOptions()).map((x) => x.ID)).toEqual([2]);
  });
});

describe("category classification", () => {
  it("assigns the expected category to each built-in option", () => {
    const byValue = Object.fromEntries(getAssetTypeOptions().map((o) => [o.value, o.category]));
    expect(byValue).toEqual({
      ssh: "servers",
      local: "servers",
      serial: "servers",
      vnc: "servers",
      rdp: "servers",
      database: "databases",
      redis: "databases",
      mongodb: "databases",
      etcd: "databases",
      oss: "databases",
      kafka: "middleware",
      k8s: "middleware",
    });
  });

  it("marks extension options as category 'extension'", () => {
    install(manifest("ext", [{ type: "foo", i18n: { name: "Foo" } }]));
    expect(getAssetTypeOptions().find((o) => o.value === "foo")!.category).toBe("extension");
  });
});

describe("buildAssetTypeGroups", () => {
  it("orders groups servers → databases → middleware → extension and drops empty groups", () => {
    const groups = buildAssetTypeGroups(getAssetTypeOptions());
    expect(groups.map((g) => g.category)).toEqual(["servers", "databases", "middleware"]);
    expect(groups[0].options.map((o) => o.value)).toEqual(["ssh", "serial", "local", "vnc", "rdp"]);
    expect(groups[1].options.map((o) => o.value)).toEqual(["database", "redis", "mongodb", "etcd", "oss"]);
  });

  it("adds the extension group once an extension is installed", () => {
    install(manifest("ext", [{ type: "foo", i18n: { name: "Foo" } }]));
    const groups = buildAssetTypeGroups(getAssetTypeOptions());
    expect(groups.map((g) => g.category)).toEqual(["servers", "databases", "middleware", "extension"]);
    expect(groups[3].options.map((o) => o.value)).toEqual(["foo"]);
  });
});

describe("filterAssetTypeOptions", () => {
  const resolve = (o: { value: string }) => o.value; // label==value for test

  it("returns all when query is blank", () => {
    const opts = getAssetTypeOptions();
    expect(filterAssetTypeOptions(opts, "  ", resolve).length).toBe(opts.length);
  });

  it("matches by value substring, case-insensitive", () => {
    expect(filterAssetTypeOptions(getAssetTypeOptions(), "RED", resolve).map((o) => o.value)).toEqual(["redis"]);
  });

  it("matches by resolved label", () => {
    const labelResolve = (o: { value: string }) => (o.value === "k8s" ? "Kubernetes" : o.value);
    expect(filterAssetTypeOptions(getAssetTypeOptions(), "kuber", labelResolve).map((o) => o.value)).toEqual(["k8s"]);
  });
});

describe("getAssetTypeLabel", () => {
  const t = (k: string) => `T(${k})`;

  it("resolves i18n-key labels via t", () => {
    expect(getAssetTypeLabel("ssh", t, getAssetTypeOptions())).toBe("T(nav.ssh)");
  });

  it("returns the raw type for unknown values", () => {
    expect(getAssetTypeLabel("nope", t, getAssetTypeOptions())).toBe("nope");
  });
});

describe("extension option i18n namespace", () => {
  const filestore = manifest("filestore", [{ type: "filestore", i18n: { name: "assetType.filestore.name" } }]);

  it("tags extension options with the ext-<name> namespace and treats label as an i18n key", () => {
    install(filestore);
    const extOpt = getAssetTypeOptions().find((o) => o.value === "filestore")!;
    expect(extOpt.labelIsI18nKey).toBe(true);
    expect(extOpt.i18nNs).toBe("ext-filestore");
    expect(extOpt.label).toBe("assetType.filestore.name");
  });

  it("resolves extension labels via the ext-<name> namespace", () => {
    install(filestore);
    const extOpt = getAssetTypeOptions().find((o) => o.value === "filestore")!;
    const t = (k: string, o?: { ns?: string }) =>
      o?.ns === "ext-filestore" && k === "assetType.filestore.name" ? "对象存储" : k;
    expect(resolveAssetTypeLabel(extOpt, t)).toBe("对象存储");
  });
});

describe("resolveAssetTypeLabel", () => {
  it("resolves built-in labels in the default namespace (no ns passed)", () => {
    const ssh = getAssetTypeOptions().find((o) => o.value === "ssh")!;
    const calls: Array<[string, { ns?: string } | undefined]> = [];
    const t = (k: string, o?: { ns?: string }) => {
      calls.push([k, o]);
      return `X(${k})`;
    };
    expect(resolveAssetTypeLabel(ssh, t)).toBe("X(nav.ssh)");
    expect(calls[0]).toEqual(["nav.ssh", undefined]);
  });
});
