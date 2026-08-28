import { act, fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { AgentBlock } from "@/components/ai/AgentBlock";
import { ErrorBlock } from "@/components/ai/ErrorBlock";
import { PermissionDialog } from "@/components/ai/PermissionDialog";
import { RetryBanner } from "@/components/ai/RetryBanner";
import { ThinkingBlock } from "@/components/ai/ThinkingBlock";
import { EventsOn } from "../../wailsjs/runtime/runtime";

describe("selectable AI diagnostic content", () => {
  it("makes error and retry details selectable", () => {
    render(<ErrorBlock block={{ type: "error", content: "request failed", errorDetail: "request-id: req-1" }} />);
    expect(screen.getByText("request failed")).toHaveClass("select-text");
    expect(screen.getByText("request-id: req-1")).toHaveClass("select-text");

    render(<RetryBanner status={{ attempt: 1, startedAt: Date.now(), delayMs: 0, cause: "HTTP 503 req-2" }} />);
    expect(screen.getByText("HTTP 503 req-2")).toHaveClass("select-text");
  });

  it("makes thinking and agent bodies selectable but keeps disclosure buttons non-selectable", () => {
    render(<ThinkingBlock block={{ type: "thinking", content: "diagnostic trace", status: "completed" }} />);
    const thinkingTrigger = screen.getByRole("button");
    expect(thinkingTrigger).toHaveClass("select-none");
    fireEvent.click(thinkingTrigger);
    expect(screen.getByText("diagnostic trace")).toHaveClass("select-text");

    render(
      <AgentBlock
        block={{ type: "agent", status: "completed", agentTask: "inspect logs", content: "found root cause" }}
      />
    );
    const agentTrigger = screen.getAllByRole("button").at(-1)!;
    expect(agentTrigger).toHaveClass("select-none");
    fireEvent.click(agentTrigger);
    expect(screen.getByText("inspect logs")).toHaveClass("select-text");
    expect(screen.getByText("found root cause")).toHaveClass("select-text");
  });

  it("makes permission request input selectable", () => {
    let permissionHandler: ((data: unknown) => void) | undefined;
    vi.mocked(EventsOn).mockImplementation(((event: string, handler: (data: unknown) => void) => {
      if (event === "ai:permission") permissionHandler = handler;
      return vi.fn();
    }) as never);
    render(<PermissionDialog />);

    act(() => permissionHandler?.({ tool_name: "Bash", input: { command: "rm test.tmp" } }));
    expect(screen.getByText("rm test.tmp")).toHaveClass("select-text");
  });
});
