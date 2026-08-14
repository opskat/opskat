import { render, screen, fireEvent } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { ToolBlock } from "@/components/ai/ToolBlock";

describe("ToolBlock result status", () => {
  it("renders an error icon for a denied tool result instead of a success icon", () => {
    render(
      <ToolBlock
        block={{
          type: "tool",
          toolName: "cp",
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
          toolName: "cp",
          content: "uploaded",
          status: "completed",
        }}
      />
    );

    expect(screen.getByLabelText("toolBlock.succeeded")).toBeInTheDocument();
    expect(screen.queryByLabelText("toolBlock.failed")).not.toBeInTheDocument();
  });
});

describe("ToolBlock safe projection", () => {
  it("collapsed summary and expanded arguments render the same safe string (no raw secret)", () => {
    render(
      <ToolBlock
        block={{
          type: "tool",
          toolName: "put_asset",
          toolInput: '{"host":"db.internal","password":"<redacted>"}',
          content: "",
          status: "completed",
        }}
      />
    );

    const container = screen.getByTestId("ai-tool-block");
    // 折叠摘要行只显示安全字符串
    expect(container.textContent).toContain("<redacted>");
    expect(container.textContent).toContain("db.internal");
    expect(container.textContent).not.toContain("SuperSecretPass123");

    // 展开参数区（formatToolInput 美化 JSON 后）仍是同一安全字符串
    fireEvent.click(screen.getByRole("button"));
    expect(container.textContent).toContain("<redacted>");
    expect(container.textContent).not.toContain("SuperSecretPass123");
  });
});

describe("ToolBlock tool icon", () => {
  it("gives the cp tool its own icon instead of falling back to the generic terminal icon", () => {
    render(
      <ToolBlock
        block={{
          type: "tool",
          toolName: "cp",
          content: "",
          status: "completed",
        }}
      />
    );

    // toolIcons 之前没有 cp 这一项，落到 Terminal 兜底；补齐之后应带上专属的 lucide class。
    expect(screen.getByRole("button").querySelector(".lucide-file-up")).not.toBeNull();
    expect(screen.getByRole("button").querySelector(".lucide-terminal")).toBeNull();
  });
});
