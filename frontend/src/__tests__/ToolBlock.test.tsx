import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { ToolBlock } from "@/components/ai/ToolBlock";

describe("ToolBlock result status", () => {
  it("renders an error icon for a denied tool result instead of a success icon", () => {
    render(
      <ToolBlock
        block={{
          type: "tool",
          toolName: "upload_file",
          content: "USER DENIED: transfer rejected",
          status: "error",
        }}
      />
    );

    expect(screen.getByLabelText("toolBlock.failed")).toBeInTheDocument();
    expect(screen.queryByLabelText("toolBlock.succeeded")).not.toBeInTheDocument();
  });

  it("renders a success icon only for a completed tool result", () => {
    render(
      <ToolBlock
        block={{
          type: "tool",
          toolName: "upload_file",
          content: "uploaded",
          status: "completed",
        }}
      />
    );

    expect(screen.getByLabelText("toolBlock.succeeded")).toBeInTheDocument();
    expect(screen.queryByLabelText("toolBlock.failed")).not.toBeInTheDocument();
  });
});
