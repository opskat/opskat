import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, act, fireEvent } from "@testing-library/react";
import { OpsctlMFADialog } from "../components/approval/OpsctlMFADialog";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { RespondOpsctlMFA, CancelOpsctlMFA } from "../../wailsjs/go/opsctl/Opsctl";

function captureHandlers() {
  const handlers = new Map<string, (data: unknown) => void>();
  vi.mocked(EventsOn).mockImplementation(((event: string, handler: (data: unknown) => void) => {
    handlers.set(event, handler);
    return vi.fn();
  }) as never);
  return handlers;
}

function fire(handlers: Map<string, (data: unknown) => void>, event: string, payload: unknown) {
  const handler = handlers.get(event);
  if (!handler) throw new Error(`${event} handler not registered`);
  act(() => handler(payload));
}

function challenge(id: string, assetName = "bastion") {
  return {
    challenge_id: id,
    asset_id: 7,
    asset_name: assetName,
    name: "Verification",
    instruction: "Enter the code from your authenticator",
    prompts: ["OTP: "],
    echo: [false],
  };
}

describe("OpsctlMFADialog", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(RespondOpsctlMFA).mockResolvedValue(undefined as never);
  });

  it("shows the challenge with a masked input and submits the answers in prompt order", async () => {
    const handlers = captureHandlers();
    render(<OpsctlMFADialog />);
    fire(handlers, "opsctl:mfa", challenge("mfa_1"));

    expect(screen.getByText(/bastion/)).toBeInTheDocument();
    expect(screen.getByText("Enter the code from your authenticator")).toBeInTheDocument();
    const input = screen.getByLabelText("OTP:");
    expect(input).toHaveAttribute("type", "password");

    fireEvent.change(input, { target: { value: "123456" } });
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "action.submit" }));
    });
    expect(RespondOpsctlMFA).toHaveBeenCalledWith("mfa_1", ["123456"]);
    expect(CancelOpsctlMFA).not.toHaveBeenCalled();
    expect(screen.queryByLabelText("OTP:")).not.toBeInTheDocument();
  });

  it("cancel reports the cancel to the backend", () => {
    const handlers = captureHandlers();
    render(<OpsctlMFADialog />);
    fire(handlers, "opsctl:mfa", challenge("mfa_2"));

    fireEvent.click(screen.getByRole("button", { name: "action.cancel" }));
    expect(CancelOpsctlMFA).toHaveBeenCalledWith("mfa_2");
    expect(RespondOpsctlMFA).not.toHaveBeenCalled();
    expect(screen.queryByLabelText("OTP:")).not.toBeInTheDocument();
  });

  it("closes without answering when the requesting opsctl is gone", () => {
    const handlers = captureHandlers();
    render(<OpsctlMFADialog />);
    fire(handlers, "opsctl:mfa", challenge("mfa_3"));
    fire(handlers, "opsctl:mfa-closed", { challenge_id: "mfa_3" });

    expect(screen.queryByLabelText("OTP:")).not.toBeInTheDocument();
    expect(CancelOpsctlMFA).not.toHaveBeenCalled();
    expect(RespondOpsctlMFA).not.toHaveBeenCalled();
  });

  it("queues concurrent challenges and shows the next one after the first is answered", async () => {
    const handlers = captureHandlers();
    render(<OpsctlMFADialog />);
    fire(handlers, "opsctl:mfa", challenge("mfa_a", "bastion-a"));
    fire(handlers, "opsctl:mfa", challenge("mfa_b", "bastion-b"));

    expect(screen.getByText(/bastion-a/)).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("OTP:"), { target: { value: "111111" } });
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "action.submit" }));
    });
    expect(screen.getByText(/bastion-b/)).toBeInTheDocument();
  });
});
