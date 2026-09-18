import { describe, expect, it } from "vitest";
import { buildK8sLogDownloadFilename, k8sLogXtermOptions } from "./k8sLogTerminalText";

describe("k8sLogXtermOptions", () => {
  it("enables proposed APIs so search decorations can highlight matches", () => {
    expect(k8sLogXtermOptions().allowProposedApi).toBe(true);
  });
});

describe("buildK8sLogDownloadFilename", () => {
  it("builds a safe log file name", () => {
    expect(buildK8sLogDownloadFilename("default", "api-server", "app")).toBe("default-api-server-app.log");
    expect(buildK8sLogDownloadFilename("kube-system", "coredns/abc", "")).toBe("kube-system-coredns_abc.log");
  });
});
