import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { NetworkProxyTestDialog } from "../../../components/settings/NetworkProxyTestDialog";
import type { NetworkProxyForm } from "../../../components/settings/networkProxyForm";
import { CancelTest, SaveNetworkProxySettings, TestNetworkProxy } from "../../../../wailsjs/go/system/System";

const KEY = "opskat.networkProxy.lastTestUrl";

const validForm: NetworkProxyForm = {
  mode: "custom",
  type: "http",
  host: "127.0.0.1",
  port: "7890",
  username: "",
  password: "",
  noProxy: "localhost",
};

type Result = Awaited<ReturnType<typeof TestNetworkProxy>>;
const result = (over: Partial<Result> = {}): Result =>
  ({
    ok: true,
    via: "http://127.0.0.1:7890",
    host: "api.openai.com",
    status: 401,
    elapsedMs: 123,
    reason: "",
    detail: "",
    ...over,
  }) as Result;

function setup(form: NetworkProxyForm = validForm) {
  const onOpenChange = vi.fn();
  render(<NetworkProxyTestDialog open form={form} onOpenChange={onOpenChange} />);
  return { onOpenChange, user: userEvent.setup() };
}

const urlBox = () => screen.getByLabelText("network.proxy.test.url");
const runBtn = () => screen.getByRole("button", { name: "network.proxy.test.run" });
const closeBtn = () => screen.getByRole("button", { name: "action.close" });

describe("NetworkProxyTestDialog", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    vi.mocked(CancelTest).mockResolvedValue(undefined as never);
  });

  it("prefills the address from the last test and starts empty otherwise", () => {
    localStorage.setItem(KEY, "https://api.openai.com");
    const { unmount } = render(<NetworkProxyTestDialog open form={validForm} onOpenChange={vi.fn()} />);
    expect(urlBox()).toHaveValue("https://api.openai.com");
    unmount();
    localStorage.clear();
    render(<NetworkProxyTestDialog open form={validForm} onOpenChange={vi.fn()} />);
    expect(urlBox()).toHaveValue("");
    expect(runBtn()).toBeDisabled();
  });

  it("refuses a non-http(s) address with an inline error", async () => {
    const { user } = setup();
    await user.type(urlBox(), "ftp://example.com");
    expect(screen.getByText("network.proxy.test.urlInvalid")).toBeInTheDocument();
    expect(runBtn()).toBeDisabled();
    await user.clear(urlBox());
    await user.type(urlBox(), "https://example.com");
    expect(screen.queryByText("network.proxy.test.urlInvalid")).not.toBeInTheDocument();
    expect(runBtn()).toBeEnabled();
  });

  it("cannot test while the proxy form is invalid and says to fix it first", () => {
    localStorage.setItem(KEY, "https://example.com");
    setup({ ...validForm, port: "99999" });
    expect(screen.getByText("network.proxy.test.fixForm")).toBeInTheDocument();
    expect(runBtn()).toBeDisabled();
  });

  it("tests the unsaved form values with a unique id, never saves, and remembers the address", async () => {
    vi.mocked(TestNetworkProxy).mockResolvedValue(result());
    const { user } = setup();
    await user.type(urlBox(), "https://api.openai.com");
    await user.click(runBtn());
    await screen.findByText("network.proxy.test.success");
    expect(TestNetworkProxy).toHaveBeenCalledWith(
      expect.any(String),
      expect.objectContaining({ mode: "custom", host: "127.0.0.1", port: 7890 }),
      "https://api.openai.com"
    );
    expect(SaveNetworkProxySettings).not.toHaveBeenCalled();
    expect(localStorage.getItem(KEY)).toBe("https://api.openai.com");

    await user.click(runBtn());
    await waitFor(() => expect(TestNetworkProxy).toHaveBeenCalledTimes(2));
    const ids = vi.mocked(TestNetworkProxy).mock.calls.map((c) => c[0]);
    expect(new Set(ids).size).toBe(2);
  });

  it("locks the button and address while testing", async () => {
    let finish!: (r: Result) => void;
    vi.mocked(TestNetworkProxy).mockReturnValue(new Promise<Result>((r) => (finish = r)));
    const { user } = setup();
    await user.type(urlBox(), "https://example.com");
    await user.click(runBtn());
    expect(screen.getByRole("button", { name: "network.proxy.test.running" })).toBeDisabled();
    expect(urlBox()).toBeDisabled();
    finish(result());
    await screen.findByText("network.proxy.test.success");
    expect(urlBox()).toBeEnabled();
  });

  it("shows via, host, status and elapsed on success, and 直连 for direct", async () => {
    vi.mocked(TestNetworkProxy).mockResolvedValue(result());
    const { user } = setup();
    await user.type(urlBox(), "https://api.openai.com");
    await user.click(runBtn());
    await screen.findByText("network.proxy.test.success");
    expect(screen.getByText("http://127.0.0.1:7890")).toBeInTheDocument();
    expect(screen.getByText("api.openai.com")).toBeInTheDocument();
    expect(screen.getByText("401")).toBeInTheDocument();
    expect(screen.getByText("123 ms")).toBeInTheDocument();

    vi.mocked(TestNetworkProxy).mockResolvedValue(result({ via: "direct" }));
    await user.click(runBtn());
    await screen.findByText("network.proxy.test.direct");
    expect(screen.queryByText("direct")).not.toBeInTheDocument();
  });

  it.each(["proxy_unreachable", "proxy_auth", "dns", "tls", "timeout", "other"])(
    "shows the %s reason with the raw detail on failure",
    async (reason) => {
      vi.mocked(TestNetworkProxy).mockResolvedValue(
        result({ ok: false, reason, detail: `raw-${reason}-error`, status: 0 })
      );
      const { user } = setup();
      await user.type(urlBox(), "https://example.com");
      await user.click(runBtn());
      await screen.findByText("network.proxy.test.failed");
      expect(screen.getByText(`network.proxy.test.reason.${reason}`)).toBeInTheDocument();
      expect(screen.getByText(`raw-${reason}-error`)).toBeInTheDocument();
      expect(screen.queryByText("network.proxy.test.success")).not.toBeInTheDocument();
    }
  );

  it("shows the backend message when the request is rejected, and can retest after", async () => {
    vi.mocked(TestNetworkProxy).mockRejectedValueOnce(new Error("端口超出范围 / port out of range"));
    const { user, onOpenChange } = setup();
    await user.type(urlBox(), "https://example.com");
    await user.click(runBtn());
    await screen.findByText("端口超出范围 / port out of range");
    expect(onOpenChange).not.toHaveBeenCalled();
    vi.mocked(TestNetworkProxy).mockResolvedValue(result());
    await user.click(runBtn());
    await screen.findByText("network.proxy.test.success");
    expect(screen.queryByText("端口超出范围 / port out of range")).not.toBeInTheDocument();
  });

  it("closing mid-test cancels it by id and discards the late result", async () => {
    let finish!: (r: Result) => void;
    vi.mocked(TestNetworkProxy).mockReturnValue(new Promise<Result>((r) => (finish = r)));
    const { user, onOpenChange } = setup();
    await user.type(urlBox(), "https://example.com");
    await user.click(runBtn());
    await user.click(closeBtn());
    const id = vi.mocked(TestNetworkProxy).mock.calls[0][0];
    expect(CancelTest).toHaveBeenCalledWith(id);
    expect(onOpenChange).toHaveBeenCalledWith(false);
    finish(result());
    await Promise.resolve();
    expect(screen.queryByText("network.proxy.test.success")).not.toBeInTheDocument();
  });

  it("closing when idle does not call CancelTest", async () => {
    const { user, onOpenChange } = setup();
    await user.click(closeBtn());
    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(CancelTest).not.toHaveBeenCalled();
  });
});
