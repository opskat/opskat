import { describe, it, expect } from "vitest";
import {
  getAssetTypeOptions,
  matchSelectedTypes,
  buildAssetTypeGroups,
  filterAssetTypeOptions,
  getAssetTypeLabel,
  resolveAssetTypeLabel,
} from "@/lib/assetTypes/options";
import { getAssetType } from "@/lib/assetTypes";
import { asset_entity } from "../../wailsjs/go/models";

describe("getAssetTypeOptions", () => {
  it("returns built-in options when extensions registry is empty", () => {
    const opts = getAssetTypeOptions({});
    const values = opts.map((o) => o.value);
    expect(values).toEqual(["ssh", "database", "redis", "mongodb", "kafka", "k8s", "serial", "local", "etcd"]);
    expect(opts.every((o) => o.group === "builtin")).toBe(true);
  });

  it("aliases on database include mysql, postgresql, database", () => {
    const opts = getAssetTypeOptions({});
    const db = opts.find((o) => o.value === "database")!;
    expect(new Set(db.aliases)).toEqual(new Set(["database", "mysql", "postgresql"]));
  });

  it("merges extension assetTypes after built-ins", () => {
    const extensions = {
      k8sExt: {
        manifest: {
          name: "k8sExt",
          version: "1.0.0",
          icon: "Server",
          i18n: { displayName: "Kubernetes", description: "" },
          assetTypes: [{ type: "kubernetes", i18n: { name: "Kubernetes" } }],
        },
      },
    };
    const opts = getAssetTypeOptions(extensions as never);
    const ext = opts.find((o) => o.value === "kubernetes");
    expect(ext).toBeTruthy();
    expect(ext!.group).toBe("extension");
    expect(ext!.label).toBe("Kubernetes");
  });

  it("ignores extensions without assetTypes", () => {
    const extensions = {
      otherExt: {
        manifest: {
          name: "otherExt",
          version: "1.0.0",
          icon: "Box",
          i18n: { displayName: "Other", description: "" },
        },
      },
    };
    const opts = getAssetTypeOptions(extensions as never);
    expect(opts.filter((o) => o.group === "extension")).toEqual([]);
  });
});

describe("built-in options derive from the registry (single source)", () => {
  it("each built-in option's icon is the same component as its registry definition's icon", () => {
    const builtins = getAssetTypeOptions({}).filter((o) => o.group === "builtin");
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
  const assets = [a(1, "ssh"), a(2, "mysql"), a(3, "postgresql"), a(4, "redis"), a(5, "kubernetes")];
  const opts = getAssetTypeOptions({
    k8sExt: {
      manifest: {
        name: "k8sExt",
        version: "1",
        icon: "Server",
        i18n: { displayName: "Kubernetes", description: "" },
        assetTypes: [{ type: "kubernetes", i18n: { name: "Kubernetes" } }],
      },
    },
  } as never);

  it("matches database aliases (mysql, postgresql)", () => {
    expect(matchSelectedTypes(assets, ["database"], opts).map((x) => x.ID)).toEqual([2, 3]);
  });

  it("matches extension type", () => {
    expect(matchSelectedTypes(assets, ["kubernetes"], opts).map((x) => x.ID)).toEqual([5]);
  });

  it("treats empty selection as no filter (returns all)", () => {
    expect(matchSelectedTypes(assets, [], opts).map((x) => x.ID)).toEqual([1, 2, 3, 4, 5]);
  });

  it("matches case-insensitively", () => {
    const assetsMixed = [a(1, "SSH"), a(2, "MySQL")];
    expect(matchSelectedTypes(assetsMixed, ["ssh"], opts).map((x) => x.ID)).toEqual([1]);
    expect(matchSelectedTypes(assetsMixed, ["database"], opts).map((x) => x.ID)).toEqual([2]);
  });
});

describe("category classification", () => {
  it("assigns the expected category to each built-in option", () => {
    const byValue = Object.fromEntries(getAssetTypeOptions({}).map((o) => [o.value, o.category]));
    expect(byValue).toEqual({
      ssh: "servers",
      local: "servers",
      serial: "servers",
      database: "databases",
      redis: "databases",
      mongodb: "databases",
      etcd: "databases",
      kafka: "middleware",
      k8s: "middleware",
    });
  });

  it("marks extension options as category 'extension'", () => {
    const opts = getAssetTypeOptions({
      ext: {
        manifest: {
          name: "ext",
          version: "1",
          icon: "Server",
          i18n: { displayName: "Foo", description: "" },
          assetTypes: [{ type: "foo", i18n: { name: "Foo" } }],
        },
      },
    } as never);
    expect(opts.find((o) => o.value === "foo")!.category).toBe("extension");
  });
});

describe("buildAssetTypeGroups", () => {
  it("orders groups servers → databases → middleware → extension and drops empty groups", () => {
    const groups = buildAssetTypeGroups(getAssetTypeOptions({}));
    expect(groups.map((g) => g.category)).toEqual(["servers", "databases", "middleware"]);
    expect(groups[0].options.map((o) => o.value)).toEqual(["ssh", "serial", "local"]);
    expect(groups[1].options.map((o) => o.value)).toEqual(["database", "redis", "mongodb", "etcd"]);
  });
});

describe("filterAssetTypeOptions", () => {
  const opts = getAssetTypeOptions({});
  const resolve = (o: { value: string }) => o.value; // label==value for test

  it("returns all when query is blank", () => {
    expect(filterAssetTypeOptions(opts, "  ", resolve).length).toBe(opts.length);
  });

  it("matches by value substring, case-insensitive", () => {
    expect(filterAssetTypeOptions(opts, "RED", resolve).map((o) => o.value)).toEqual(["redis"]);
  });

  it("matches by resolved label", () => {
    const labelResolve = (o: { value: string }) => (o.value === "k8s" ? "Kubernetes" : o.value);
    expect(filterAssetTypeOptions(opts, "kuber", labelResolve).map((o) => o.value)).toEqual(["k8s"]);
  });
});

describe("getAssetTypeLabel", () => {
  const opts = getAssetTypeOptions({});
  const t = (k: string) => `T(${k})`;

  it("resolves i18n-key labels via t", () => {
    expect(getAssetTypeLabel("ssh", t, opts)).toBe("T(nav.ssh)");
  });

  it("returns the raw type for unknown values", () => {
    expect(getAssetTypeLabel("nope", t, opts)).toBe("nope");
  });
});

const extManifest = {
  oss: {
    manifest: {
      name: "oss",
      version: "1",
      icon: "Server",
      i18n: { displayName: "OSS", description: "" },
      assetTypes: [{ type: "oss", i18n: { name: "assetType.oss.name" } }],
    },
  },
};

describe("extension option i18n namespace", () => {
  it("tags extension options with the ext-<name> namespace and treats label as an i18n key", () => {
    const ossOpt = getAssetTypeOptions(extManifest as never).find((o) => o.value === "oss")!;
    expect(ossOpt.labelIsI18nKey).toBe(true);
    expect(ossOpt.i18nNs).toBe("ext-oss");
    expect(ossOpt.label).toBe("assetType.oss.name");
  });
});

describe("resolveAssetTypeLabel", () => {
  it("resolves built-in labels in the default namespace (no ns passed)", () => {
    const ssh = getAssetTypeOptions({}).find((o) => o.value === "ssh")!;
    const calls: Array<[string, { ns?: string } | undefined]> = [];
    const t = (k: string, o?: { ns?: string }) => {
      calls.push([k, o]);
      return `X(${k})`;
    };
    expect(resolveAssetTypeLabel(ssh, t)).toBe("X(nav.ssh)");
    expect(calls[0]).toEqual(["nav.ssh", undefined]);
  });

  it("resolves extension labels via the ext-<name> namespace", () => {
    const ossOpt = getAssetTypeOptions(extManifest as never).find((o) => o.value === "oss")!;
    const t = (k: string, o?: { ns?: string }) => (o?.ns === "ext-oss" && k === "assetType.oss.name" ? "对象存储" : k);
    expect(resolveAssetTypeLabel(ossOpt, t)).toBe("对象存储");
  });
});
