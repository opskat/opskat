import { describe, it, expect } from "vitest";
import { prefixLeafName, flattenPrefixTree, type OssPrefixNode } from "../ossPrefixTree";

describe("prefixLeafName", () => {
  it("returns the last path segment of a trailing-slash prefix", () => {
    expect(prefixLeafName("a/b/c/")).toBe("c");
  });
  it("handles a single top-level prefix", () => {
    expect(prefixLeafName("a/")).toBe("a");
  });
  it("returns empty string for the root", () => {
    expect(prefixLeafName("")).toBe("");
  });
});

describe("flattenPrefixTree", () => {
  const tree: Record<string, OssPrefixNode> = {
    "": { childPrefixes: ["a/", "b/"], loaded: true, cursor: "", truncated: false },
    "a/": { childPrefixes: ["a/x/"], loaded: true, cursor: "", truncated: false },
  };

  it("lists only root children when nothing is expanded", () => {
    const rows = flattenPrefixTree(tree, new Set(), "");
    expect(rows).toEqual([
      { depth: 0, name: "a", prefix: "a/", isExpanded: false, loaded: true },
      { depth: 0, name: "b", prefix: "b/", isExpanded: false, loaded: false },
    ]);
  });

  it("recurses into an expanded node using that node's lazily-loaded children", () => {
    const rows = flattenPrefixTree(tree, new Set(["a/"]), "");
    expect(rows.map((r) => `${r.depth}:${r.prefix}`)).toEqual(["0:a/", "1:a/x/", "0:b/"]);
    expect(rows[0].isExpanded).toBe(true);
  });

  it("does not recurse into an expanded node whose children are not loaded yet", () => {
    const partial: Record<string, OssPrefixNode> = {
      "": { childPrefixes: ["a/"], loaded: true, cursor: "", truncated: false },
    };
    const rows = flattenPrefixTree(partial, new Set(["a/"]), "");
    expect(rows).toEqual([{ depth: 0, name: "a", prefix: "a/", isExpanded: true, loaded: false }]);
  });
});
