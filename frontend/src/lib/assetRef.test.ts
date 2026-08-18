import { describe, expect, it, beforeEach } from "vitest";
import {
  formatAssetMarkdownRef,
  parseOpsctlAssetHref,
  parseOpsctlAssetMarkdown,
  shouldCopyAssetRef,
  splitOpsctlAssetRefs,
} from "./assetRef";
import { useAssetStore } from "@/stores/assetStore";

describe("formatAssetMarkdownRef", () => {
  it("formats a name and id as a markdown opsctl link", () => {
    expect(formatAssetMarkdownRef("web-01", 1)).toBe("[web-01](opsctl://asset/1)");
  });

  it("escapes markdown link-text metacharacters in the asset name", () => {
    expect(formatAssetMarkdownRef("prod [web]", 9)).toBe("[prod \\[web\\]](opsctl://asset/9)");
    expect(formatAssetMarkdownRef("a\\b", 2)).toBe("[a\\\\b](opsctl://asset/2)");
  });
});

describe("shouldCopyAssetRef", () => {
  beforeEach(() => {
    useAssetStore.setState({ selectedAssetId: 1 });
  });

  it("allows copy from a non-editable target when an asset is selected", () => {
    const div = document.createElement("div");
    document.body.appendChild(div);
    expect(shouldCopyAssetRef(div)).toBe(true);
  });

  it("does not steal copy from an input", () => {
    const input = document.createElement("input");
    document.body.appendChild(input);
    expect(shouldCopyAssetRef(input)).toBe(false);
  });

  it("does not steal copy from a terminal", () => {
    const xterm = document.createElement("div");
    xterm.className = "xterm";
    const ta = document.createElement("textarea");
    xterm.appendChild(ta);
    document.body.appendChild(xterm);
    expect(shouldCopyAssetRef(ta)).toBe(false);
  });

  it("does nothing when no asset is selected", () => {
    useAssetStore.setState({ selectedAssetId: null });
    const div = document.createElement("div");
    document.body.appendChild(div);
    expect(shouldCopyAssetRef(div)).toBe(false);
  });
});

describe("parseOpsctlAssetHref", () => {
  it("extracts the numeric id from an opsctl asset URI", () => {
    expect(parseOpsctlAssetHref("opsctl://asset/1")).toBe(1);
    expect(parseOpsctlAssetHref("OPSCTL://asset/42")).toBe(42);
  });

  it("rejects other schemes and shapes", () => {
    expect(parseOpsctlAssetHref("https://opskat.dev")).toBeNull();
    expect(parseOpsctlAssetHref("opsctl://group/1")).toBeNull();
    expect(parseOpsctlAssetHref("opsctl://asset/web-01")).toBeNull();
  });
});

describe("parseOpsctlAssetMarkdown", () => {
  it("extracts name and id from a copied markdown ref", () => {
    expect(parseOpsctlAssetMarkdown("[web-01](opsctl://asset/1)")).toEqual({ name: "web-01", id: 1 });
  });

  it("unescapes markdown link-text metacharacters", () => {
    expect(parseOpsctlAssetMarkdown("[prod \\[web\\]](opsctl://asset/9)")).toEqual({
      name: "prod [web]",
      id: 9,
    });
  });
});

describe("splitOpsctlAssetRefs", () => {
  it("keeps ordinary text unchanged", () => {
    expect(splitOpsctlAssetRefs("check disk")).toEqual([{ type: "text", text: "check disk" }]);
  });

  it("splits markdown refs and bare URIs into mention-shaped parts", () => {
    expect(splitOpsctlAssetRefs("look at [web-01](opsctl://asset/1) and opsctl://asset/2")).toEqual([
      { type: "text", text: "look at " },
      { type: "ref", name: "web-01", id: 1 },
      { type: "text", text: " and " },
      { type: "ref", name: "2", id: 2 },
    ]);
  });
});
