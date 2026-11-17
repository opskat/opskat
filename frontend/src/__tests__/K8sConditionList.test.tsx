import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { K8sConditionList } from "@/components/k8s/K8sConditionList";

const pill = (text: string) => screen.getByText(text);

describe("K8sConditionList colours", () => {
  it("colours a normal condition True as success", () => {
    render(<K8sConditionList conditions={[{ type: "Ready", status: "True" }]} />);
    expect(pill("True").className).toContain("text-success");
  });

  it("colours a normal condition False as error", () => {
    render(<K8sConditionList conditions={[{ type: "Ready", status: "False" }]} />);
    expect(pill("False").className).toContain("text-destructive");
  });

  it("colours Unknown as neutral", () => {
    render(<K8sConditionList conditions={[{ type: "Ready", status: "Unknown" }]} />);
    expect(pill("Unknown").className).toContain("text-muted-foreground");
  });

  it.each(["MemoryPressure", "DiskPressure", "PIDPressure", "NetworkUnavailable"])(
    "colours problem condition %s False as success and True as error",
    (type) => {
      const { unmount } = render(<K8sConditionList conditions={[{ type, status: "False" }]} />);
      expect(pill("False").className).toContain("text-success");
      unmount();
      render(<K8sConditionList conditions={[{ type, status: "True" }]} />);
      expect(pill("True").className).toContain("text-destructive");
    }
  );
});
