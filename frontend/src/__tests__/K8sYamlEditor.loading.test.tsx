import { useState } from "react";
import { act, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { K8sYamlEditor } from "@/components/k8s/yaml/K8sYamlEditor";

const h = vi.hoisted(() => ({ mount: null as null | (() => void) }));
vi.mock("@/components/CodeEditor", () => ({
  // The real editor is lazy: it reports onMount only once Monaco has loaded. Here the test decides when.
  CodeEditor: ({ onMount }: { onMount?: (editor: unknown, monaco: unknown) => void }) => {
    const [, setTick] = useState(0);
    h.mount = () => {
      onMount?.({ getModel: () => null }, {});
      setTick((n) => n + 1);
    };
    return <div data-testid="yaml-editor" />;
  },
}));

describe("K8sYamlEditor loading", () => {
  it("shows a loading indicator until the editor has mounted", () => {
    render(<K8sYamlEditor value="" onChange={() => {}} />);
    expect(screen.getByRole("status")).toBeInTheDocument();
    act(() => h.mount?.());
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });
});
