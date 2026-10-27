import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { toast } from "sonner";
import { TooltipProvider } from "@opskat/ui";
import { NetworkSection } from "../../../components/settings/NetworkSection";
import { GetNetworkProxySettings, SaveNetworkProxySettings } from "../../../../wailsjs/go/system/System";
import { notifySuccess } from "@/lib/notify";

vi.mock("@/lib/notify", () => ({ notifySuccess: vi.fn(), notifyCopied: vi.fn() }));
vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));

type View = Awaited<ReturnType<typeof GetNetworkProxySettings>>;

const view = (over: Partial<View> = {}): View =>
  ({
    mode: "env",
    type: "http",
    host: "",
    port: 0,
    username: "",
    passwordSet: false,
    noProxy: "localhost\n127.0.0.1",
    envVars: [],
    ...over,
  }) as View;

async function renderWith(v: View) {
  vi.mocked(GetNetworkProxySettings).mockResolvedValue(v);
  render(
    <TooltipProvider>
      <NetworkSection />
    </TooltipProvider>
  );
  await screen.findByText("network.proxy.mode");
}

async function chooseMode(user: ReturnType<typeof userEvent.setup>, label: string) {
  await user.click(screen.getAllByRole("combobox")[0]);
  await user.click(await screen.findByRole("option", { name: label }));
}

describe("NetworkSection", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(SaveNetworkProxySettings).mockResolvedValue(undefined as never);
  });

  it("env mode lists the detected variables and no custom fields", async () => {
    await renderWith(
      view({ envVars: [{ name: "HTTPS_PROXY", value: "http://u:***@127.0.0.1:7890" }] as View["envVars"] })
    );
    expect(screen.getByText(/HTTPS_PROXY=http:\/\/u:\*\*\*@127\.0\.0\.1:7890/)).toBeInTheDocument();
    expect(screen.queryByText("network.proxy.envNone")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("network.proxy.host")).not.toBeInTheDocument();
  });

  it("env mode warns when no variable is detected", async () => {
    await renderWith(view());
    expect(screen.getByText("network.proxy.envNone")).toBeInTheDocument();
  });

  it("direct mode shows neither env status nor custom fields", async () => {
    const user = userEvent.setup();
    await renderWith(view());
    await chooseMode(user, "network.proxy.modeDirect");
    expect(screen.queryByText("network.proxy.envNone")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("network.proxy.host")).not.toBeInTheDocument();
  });

  it("custom mode shows the fields prefilled from the saved settings", async () => {
    await renderWith(view({ mode: "custom", host: "127.0.0.1", port: 7890, username: "ops" }));
    expect(screen.getByLabelText("network.proxy.host")).toHaveValue("127.0.0.1");
    expect(screen.getByLabelText("network.proxy.port")).toHaveValue(7890);
    expect(screen.getByLabelText("network.proxy.username")).toHaveValue("ops");
    expect(screen.getByLabelText("network.proxy.noProxy")).toHaveValue("localhost\n127.0.0.1");
  });

  it("shows the unchanged-password placeholder only when a password is saved", async () => {
    await renderWith(view({ mode: "custom", host: "h", port: 1, passwordSet: true }));
    expect(screen.getByLabelText("network.proxy.password")).toHaveAttribute("placeholder", "asset.passwordUnchanged");
  });

  it("has no unchanged-password placeholder when no password is saved", async () => {
    await renderWith(view({ mode: "custom", host: "h", port: 1 }));
    expect(screen.getByLabelText("network.proxy.password")).not.toHaveAttribute(
      "placeholder",
      "asset.passwordUnchanged"
    );
  });

  it("keeps custom input across mode switches and does not save on switching", async () => {
    const user = userEvent.setup();
    await renderWith(view({ mode: "custom", host: "127.0.0.1", port: 7890 }));
    await user.clear(screen.getByLabelText("network.proxy.host"));
    await user.type(screen.getByLabelText("network.proxy.host"), "proxy.local");
    await chooseMode(user, "network.proxy.modeDirect");
    await chooseMode(user, "network.proxy.modeCustom");
    expect(screen.getByLabelText("network.proxy.host")).toHaveValue("proxy.local");
    expect(SaveNetworkProxySettings).not.toHaveBeenCalled();
  });

  it("shows inline errors under the fields and refuses to save an invalid custom form", async () => {
    const user = userEvent.setup();
    await renderWith(view({ mode: "custom", host: "", port: 0 }));
    await user.click(screen.getByRole("button", { name: /action.save/ }));
    expect(screen.getByText("network.proxy.error.hostRequired")).toBeInTheDocument();
    expect(screen.getByText("network.proxy.error.portRequired")).toBeInTheDocument();
    expect(screen.getByLabelText("network.proxy.host")).toHaveAttribute("aria-invalid", "true");
    expect(SaveNetworkProxySettings).not.toHaveBeenCalled();
  });

  it("flags an out-of-range port and an unrecognised bypass line while typing", async () => {
    const user = userEvent.setup();
    await renderWith(view({ mode: "custom", host: "127.0.0.1", port: 7890 }));
    const port = screen.getByLabelText("network.proxy.port");
    await user.clear(port);
    await user.type(port, "78900");
    expect(screen.getByText("network.proxy.error.portRange")).toBeInTheDocument();
    const noProxy = screen.getByLabelText("network.proxy.noProxy");
    await user.clear(noProxy);
    await user.type(noProxy, "ok.com{Enter}bad host");
    expect(screen.getByText("network.proxy.error.noProxyLine")).toBeInTheDocument();
  });

  it("saves the form as a NetworkProxyInput and announces success, then clears the password box", async () => {
    const user = userEvent.setup();
    await renderWith(view({ mode: "custom", type: "http", host: "127.0.0.1", port: 7890 }));
    await user.type(screen.getByLabelText("network.proxy.username"), "ops");
    await user.type(screen.getByLabelText("network.proxy.password"), "s3cret");
    vi.mocked(GetNetworkProxySettings).mockResolvedValue(
      view({ mode: "custom", host: "127.0.0.1", port: 7890, username: "ops", passwordSet: true })
    );
    await user.click(screen.getByRole("button", { name: /action.save/ }));
    await waitFor(() =>
      expect(SaveNetworkProxySettings).toHaveBeenCalledWith({
        mode: "custom",
        type: "http",
        host: "127.0.0.1",
        port: 7890,
        username: "ops",
        password: "s3cret",
        noProxy: "localhost\n127.0.0.1",
      })
    );
    await waitFor(() => expect(notifySuccess).toHaveBeenCalledWith("network.proxy.saved"));
    await waitFor(() =>
      expect(screen.getByLabelText("network.proxy.password")).toHaveAttribute("placeholder", "asset.passwordUnchanged")
    );
    expect(screen.getByLabelText("network.proxy.password")).toHaveValue("");
  });

  it("saves direct mode without validating the hidden custom fields", async () => {
    const user = userEvent.setup();
    await renderWith(view());
    await chooseMode(user, "network.proxy.modeDirect");
    await user.click(screen.getByRole("button", { name: /action.save/ }));
    await waitFor(() =>
      expect(SaveNetworkProxySettings).toHaveBeenCalledWith(expect.objectContaining({ mode: "direct" }))
    );
  });

  it("surfaces a backend rejection with toast.error and no success toast", async () => {
    vi.mocked(SaveNetworkProxySettings).mockRejectedValue(new Error("port out of range"));
    const user = userEvent.setup();
    await renderWith(view({ mode: "custom", host: "127.0.0.1", port: 7890 }));
    await user.click(screen.getByRole("button", { name: /action.save/ }));
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("port out of range"));
    expect(notifySuccess).not.toHaveBeenCalled();
  });

  it("does not render a test-connection button yet", async () => {
    await renderWith(view());
    expect(screen.queryByRole("button", { name: /test/i })).not.toBeInTheDocument();
  });
});
