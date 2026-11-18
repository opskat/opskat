import { render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { K8sYamlEditor } from "@/components/k8s/yaml/K8sYamlEditor";

// Monaco fails to load: the shared editor then shows the failure with a retry, which must not stay hidden
// behind the YAML editor's loading indicator.
vi.mock("@/lib/monaco-setup", () => ({
  setupMonaco: () => {
    throw new Error("chunk load failed");
  },
}));

describe("K8sYamlEditor when the editor cannot load", () => {
  it("replaces the loading indicator with the editor's failure and retry", async () => {
    render(<K8sYamlEditor value="" onChange={() => {}} />);
    await waitFor(() => expect(screen.getByText("codeEditor.loadFailed")).toBeInTheDocument());
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "action.retry" })).toBeVisible();
  });
});
