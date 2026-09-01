import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ConnectionProgress } from "@/components/terminal/ConnectionProgress";
import { useTerminalStore, type ConnectionState } from "@/stores/terminalStore";
import { useAssetStore } from "@/stores/assetStore";
import {
  CancelSSHConnect,
  RespondAuthChallenge,
  RespondHostKeyVerify,
  UpdateAssetPassword,
  ConnectSSHAsync,
} from "../../../../wailsjs/go/ssh/SSH";
import { asset_entity } from "../../../../wailsjs/go/models";

vi.mock("../../../../wailsjs/go/ssh/SSH", () => ({
  ConnectSSHAsync: vi.fn().mockResolvedValue("new-conn"),
  RespondAuthChallenge: vi.fn(),
  RespondHostKeyVerify: vi.fn(),
  CancelSSHConnect: vi.fn(),
  UpdateAssetPassword: vi.fn().mockResolvedValue(undefined),
  DisconnectSSH: vi.fn(),
  GetSSHSyncState: vi.fn(),
  ResizeSSH: vi.fn(),
  SplitSSH: vi.fn(),
  WriteSSH: vi.fn(),
}));

vi.mock("../../../../wailsjs/runtime/runtime", () => ({
  EventsOn: vi.fn(),
  EventsOff: vi.fn(),
}));

function challengeConnection(over: Partial<ConnectionState> = {}): ConnectionState {
  return {
    connectionId: "conn-1",
    assetId: 1,
    assetName: "Server 1",
    transport: "ssh",
    password: "",
    logs: [],
    status: "auth_challenge",
    currentStep: "auth",
    challenge: {
      challengeId: "ch-1",
      prompts: ["Password:", "Verification code:"],
      echo: [false, true],
    },
    ...over,
  };
}

function errorConnection(over: Partial<ConnectionState> = {}): ConnectionState {
  return {
    connectionId: "conn-1",
    assetId: 1,
    assetName: "Server 1",
    transport: "ssh",
    password: "",
    logs: [],
    status: "error",
    currentStep: "auth",
    error: "unable to authenticate",
    ...over,
  };
}

function hostKeyConnection(): ConnectionState {
  return {
    connectionId: "conn-1",
    assetId: 1,
    assetName: "Server 1",
    transport: "ssh",
    password: "",
    logs: [],
    status: "host_key_verify",
    currentStep: "auth",
    hostKeyVerify: {
      verifyId: "verify-1",
      host: "server.example.com",
      port: 22,
      keyType: "ssh-ed25519",
      fingerprint: "SHA256:new-fingerprint",
      oldFingerprint: "SHA256:old-fingerprint",
      isChanged: true,
    },
  };
}

beforeEach(() => {
  useTerminalStore.setState({ connections: {}, tabData: {}, sessionSync: {}, connectingAssetIds: new Set() });
  useAssetStore.setState({ assets: [] });
  vi.clearAllMocks();
});

describe("ConnectionProgress MFA 挑战", () => {
  it("主机与新旧指纹可由用户原生选中复制", () => {
    useTerminalStore.setState({ connections: { "conn-1": hostKeyConnection() } });
    render(<ConnectionProgress connectionId="conn-1" isTabActive isPaneActive />);

    expect(screen.getByText("server.example.com:22").closest("div")).toHaveClass("select-text");
    expect(screen.getByText("SHA256:new-fingerprint")).toHaveClass("select-text");
    expect(screen.getByText("SHA256:old-fingerprint")).toHaveClass("select-text");
  });

  it.each([
    ["ssh-host-key-reject", 2],
    ["ssh-host-key-accept-once", 1],
    ["ssh-host-key-trust", 0],
  ])("复用身份提示后 %s 仍发送 SSH 动作 %i", async (testId, action) => {
    useTerminalStore.setState({ connections: { "conn-1": hostKeyConnection() } });
    const u = userEvent.setup();
    render(<ConnectionProgress connectionId="conn-1" isTabActive isPaneActive />);

    await u.click(screen.getByTestId(testId));

    expect(RespondHostKeyVerify).toHaveBeenCalledWith("verify-1", action);
  });
  it("标签与输入框正确关联,提示按服务器顺序渲染", () => {
    useTerminalStore.setState({ connections: { "conn-1": challengeConnection() } });
    render(<ConnectionProgress connectionId="conn-1" isTabActive isPaneActive />);

    const labels = screen.getAllByText(/Password:|Verification code:/);
    expect(labels.map((l) => l.textContent)).toEqual(["Password:", "Verification code:"]);

    // 标签与输入框正确关联(htmlFor ↔ id),隐藏提示为密码框、echo=true 为文本框。
    const inputs = labels.map((label) => {
      const htmlFor = label.getAttribute("for");
      expect(htmlFor).toBeTruthy();
      const input = document.getElementById(htmlFor!);
      expect(input).toBeTruthy();
      return input as HTMLInputElement;
    });
    expect(inputs).toHaveLength(2);
    expect(inputs[0].type).toBe("password");
    expect(inputs[1].type).toBe("text");
  });

  it("服务器文本按普通文本渲染,不解析为 HTML", () => {
    useTerminalStore.setState({
      connections: {
        "conn-1": challengeConnection({
          challenge: { challengeId: "ch-1", prompts: ['<img src=x onerror="window.__x=1">'], echo: [true] },
        }),
      },
    });
    render(<ConnectionProgress connectionId="conn-1" isTabActive isPaneActive />);
    const label = screen.getByText('<img src=x onerror="window.__x=1">');
    expect(label).toBeInTheDocument();
    expect(document.querySelector("img")).toBeNull();
    expect((window as { __x?: number }).__x).toBeUndefined();
  });

  it("Agent 结构化 MFA 的挑战名称与说明按普通文本展示(不解析为 HTML)", () => {
    useTerminalStore.setState({
      connections: {
        "conn-1": challengeConnection({
          challenge: {
            challengeId: "ch-1",
            name: '<img src=x onerror="window.__y=1">Verification',
            instruction: "Enter the code shown on your device",
            prompts: ["Code:"],
            echo: [false],
          },
        }),
      },
    });
    render(<ConnectionProgress connectionId="conn-1" isTabActive isPaneActive />);
    expect(screen.getByText("Enter the code shown on your device")).toBeInTheDocument();
    expect(screen.getByText(/Verification/)).toBeInTheDocument();
    // 名称/说明/提示都按普通文本渲染,不产生可注入的 HTML 元素。
    expect(document.querySelector("img")).toBeNull();
    expect((window as { __y?: number }).__y).toBeUndefined();
  });

  it("Enter 提交当前挑战(answers 按提示顺序)", async () => {
    useTerminalStore.setState({ connections: { "conn-1": challengeConnection() } });
    const u = userEvent.setup();
    render(<ConnectionProgress connectionId="conn-1" isTabActive isPaneActive />);

    const inputs = screen.getAllByLabelText(/Password:|Verification code:/);
    await u.type(inputs[0], "s3cret");
    await u.type(inputs[1], "123456");
    await u.keyboard("{Enter}");

    expect(RespondAuthChallenge).toHaveBeenCalledWith("ch-1", ["s3cret", "123456"]);
  });

  it("Esc 取消当前操作(取消连接,不提交答案)", async () => {
    useTerminalStore.setState({ connections: { "conn-1": challengeConnection() } });
    const u = userEvent.setup();
    render(<ConnectionProgress connectionId="conn-1" isTabActive isPaneActive />);

    const inputs = screen.getAllByLabelText(/Password:|Verification code:/);
    await u.type(inputs[0], "s3cret");
    await u.keyboard("{Escape}");

    expect(CancelSSHConnect).toHaveBeenCalledWith("conn-1");
    expect(RespondAuthChallenge).not.toHaveBeenCalled();
  });

  it("隐藏标签页不抢夺焦点:非活动时不自动聚焦;活动时首输入框聚焦", () => {
    useTerminalStore.setState({ connections: { "conn-1": challengeConnection() } });
    const { unmount } = render(<ConnectionProgress connectionId="conn-1" isTabActive={false} isPaneActive />);
    const inputs = screen.getAllByLabelText(/Password:|Verification code:/);
    expect(document.activeElement).not.toBe(inputs[0]);
    unmount();

    useTerminalStore.setState({ connections: { "conn-1": challengeConnection() } });
    render(<ConnectionProgress connectionId="conn-1" isTabActive isPaneActive />);
    expect(document.activeElement).toBe(screen.getAllByLabelText(/Password:|Verification code:/)[0]);
  });

  it("错误后不保留已输入答案(挑战表单卸载,答案不留在任何输入框)", async () => {
    useTerminalStore.setState({ connections: { "conn-1": challengeConnection() } });
    const u = userEvent.setup();
    render(<ConnectionProgress connectionId="conn-1" isTabActive isPaneActive />);

    const inputs = screen.getAllByLabelText(/Password:|Verification code:/);
    await u.type(inputs[0], "s3cret");

    // 后端报错 → 连接进入 error,挑战表单卸载,已输入答案无处残留。
    useTerminalStore.setState({
      connections: { "conn-1": errorConnection({ error: "ssh_agent_mfa_failed: wrong code" }) },
    });
    await waitFor(() => expect(screen.queryByDisplayValue("s3cret")).not.toBeInTheDocument());
    expect(screen.queryByLabelText(/Password:|Verification code:/)).not.toBeInTheDocument();
  });

  it("Agent 错误绝不打开密码更新对话框,也不调用密码重试接口", async () => {
    useTerminalStore.setState({
      connections: {
        "conn-1": errorConnection({ error: "ssh_agent_mfa_failed: wrong code", authFailed: true }),
      },
    });
    useAssetStore.setState({
      assets: [new asset_entity.Asset({ ID: 1, Name: "Server 1", Type: "ssh", Config: "{}" })],
    });
    const u = userEvent.setup();
    render(<ConnectionProgress connectionId="conn-1" isTabActive isPaneActive />);

    // 即使后端误置 authFailed,Agent 错误也不渲染密码输入框。
    expect(screen.queryByText("ssh.password")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("ssh.password")).not.toBeInTheDocument();

    // 点击重连也不会携带密码(不调用 UpdateAssetPassword)。
    await u.click(screen.getByText("ssh.connectProgress.retry"));
    expect(UpdateAssetPassword).not.toHaveBeenCalled();
    expect(ConnectSSHAsync).toHaveBeenCalled();
  });

  it("非 Agent 认证失败仍显示密码重试(守卫不过度拦截)", async () => {
    useTerminalStore.setState({ connections: { "conn-1": errorConnection({ authFailed: true }) } });
    useAssetStore.setState({
      assets: [new asset_entity.Asset({ ID: 1, Name: "Server 1", Type: "ssh", Config: "{}" })],
    });
    const u = userEvent.setup();
    render(<ConnectionProgress connectionId="conn-1" isTabActive isPaneActive />);

    expect(screen.getByText("ssh.password")).toBeInTheDocument();
    const input = screen.getByLabelText("ssh.password");
    await u.type(input, "pw123");
    await u.keyboard("{Enter}");
    expect(UpdateAssetPassword).toHaveBeenCalledWith(1, "pw123");
  });
});
