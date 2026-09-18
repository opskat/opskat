import { describe, expect, it } from "vitest";
import {
  formatK8sMemory,
  formatK8sVersion,
  parseK8sQuantityToBytes,
  resolveK8sPreferredNamespace,
} from "./utils";

describe("parseK8sQuantityToBytes", () => {
  it("parses binary memory suffixes", () => {
    expect(parseK8sQuantityToBytes("15815400Ki")).toBe(15815400 * 1024);
    expect(parseK8sQuantityToBytes("512Mi")).toBe(512 * 1024 ** 2);
  });

  it("parses plain byte counts", () => {
    expect(parseK8sQuantityToBytes("4096")).toBe(4096);
  });
});

describe("resolveK8sPreferredNamespace", () => {
  const namespaces = [{ name: "default" }, { name: "kube-system" }];

  it("uses configured namespace when present in the cluster", () => {
    expect(resolveK8sPreferredNamespace("kube-system", namespaces)).toBe("kube-system");
  });

  it("falls back to default when asset namespace is not configured", () => {
    expect(resolveK8sPreferredNamespace("", namespaces)).toBe("default");
  });

  it("falls back to default when configured namespace is missing from the cluster", () => {
    expect(resolveK8sPreferredNamespace("missing", namespaces)).toBe("default");
  });
});

describe("formatK8sVersion", () => {
  it("does not duplicate a leading v from the API", () => {
    expect(formatK8sVersion("v1.36.2-aliyun.1")).toBe("v1.36.2-aliyun.1");
  });

  it("adds v when the version string has no prefix", () => {
    expect(formatK8sVersion("1.34.1")).toBe("v1.34.1");
  });
});

describe("formatK8sMemory", () => {
  it("formats large Ki quantities to GB", () => {
    expect(formatK8sMemory("15815400Ki")).toBe("15.1 GB");
  });

  it("returns the original string when parsing fails", () => {
    expect(formatK8sMemory("unknown")).toBe("unknown");
  });
});
