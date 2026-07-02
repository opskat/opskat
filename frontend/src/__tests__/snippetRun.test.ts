import { describe, it, expect } from "vitest";
import { RUNNABLE_ASSET_TYPES, isRunnableCategoryId } from "../components/snippet/snippetRun";

type Cat = { id: string; assetType: string };

const CATEGORIES: Cat[] = [
  { id: "shell", assetType: "ssh" },
  { id: "sql", assetType: "database" },
  { id: "mongo", assetType: "mongodb" },
  { id: "redis", assetType: "redis" },
  { id: "k8s", assetType: "k8s" },
  { id: "prompt", assetType: "" },
];

describe("isRunnableCategoryId", () => {
  it("is true for ssh / database / mongodb backed categories", () => {
    expect(isRunnableCategoryId("shell", CATEGORIES)).toBe(true);
    expect(isRunnableCategoryId("sql", CATEGORIES)).toBe(true);
    expect(isRunnableCategoryId("mongo", CATEGORIES)).toBe(true);
  });

  it("is false for non-runnable categories (redis / k8s / prompt)", () => {
    expect(isRunnableCategoryId("redis", CATEGORIES)).toBe(false);
    expect(isRunnableCategoryId("k8s", CATEGORIES)).toBe(false);
    expect(isRunnableCategoryId("prompt", CATEGORIES)).toBe(false);
  });

  it("is false for an unknown category id", () => {
    expect(isRunnableCategoryId("does-not-exist", CATEGORIES)).toBe(false);
  });

  it("exposes the canonical runnable asset-type set", () => {
    expect(RUNNABLE_ASSET_TYPES.has("ssh")).toBe(true);
    expect(RUNNABLE_ASSET_TYPES.has("database")).toBe(true);
    expect(RUNNABLE_ASSET_TYPES.has("mongodb")).toBe(true);
    expect(RUNNABLE_ASSET_TYPES.has("redis")).toBe(false);
  });
});
