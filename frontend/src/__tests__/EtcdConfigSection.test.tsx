import { describe, it, expect, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { EtcdConfigSection, type EtcdConfigSectionProps } from "@/components/asset/EtcdConfigSection";

function makeProps(overrides: Partial<EtcdConfigSectionProps> = {}): EtcdConfigSectionProps {
  return {
    endpoints: "",
    setEndpoints: vi.fn(),
    username: "",
    setUsername: vi.fn(),
    password: "",
    setPassword: vi.fn(),
    encryptedPassword: "",
    passwordSource: "inline",
    setPasswordSource: vi.fn(),
    passwordCredentialId: 0,
    setPasswordCredentialId: vi.fn(),
    managedPasswords: [],
    tls: false,
    setTls: vi.fn(),
    tlsInsecure: false,
    setTlsInsecure: vi.fn(),
    tlsServerName: "",
    setTlsServerName: vi.fn(),
    tlsCAFile: "",
    setTlsCAFile: vi.fn(),
    tlsCertFile: "",
    setTlsCertFile: vi.fn(),
    tlsKeyFile: "",
    setTlsKeyFile: vi.fn(),
    dialTimeoutSeconds: 5,
    setDialTimeoutSeconds: vi.fn(),
    commandTimeoutSeconds: 10,
    setCommandTimeoutSeconds: vi.fn(),
    sshTunnelId: 0,
    setSshTunnelId: vi.fn(),
    ...overrides,
  };
}

describe("EtcdConfigSection", () => {
  it("renders endpoints textarea with placeholder", () => {
    render(<EtcdConfigSection {...makeProps()} />);
    const textarea = screen.getByPlaceholderText(/10\.0\.0\.1:2379/);
    expect(textarea).toBeInTheDocument();
  });

  it("hides TLS sub-fields when tls=false and shows them when tls=true", () => {
    const props = makeProps({ tls: false });
    const { rerender } = render(<EtcdConfigSection {...props} />);
    // i18n is mocked to identity in setup.ts; assert on key strings
    expect(screen.queryByText("etcd.form.tlsServerName")).not.toBeInTheDocument();
    expect(screen.queryByText("etcd.form.tlsCAFile")).not.toBeInTheDocument();

    rerender(<EtcdConfigSection {...makeProps({ tls: true })} />);
    expect(screen.getByText("etcd.form.tlsServerName")).toBeInTheDocument();
    expect(screen.getByText("etcd.form.tlsCAFile")).toBeInTheDocument();
    expect(screen.getByText("etcd.form.tlsCertFile")).toBeInTheDocument();
    expect(screen.getByText("etcd.form.tlsKeyFile")).toBeInTheDocument();
    expect(screen.getByText("etcd.form.tlsInsecure")).toBeInTheDocument();
  });

  it("calls setEndpoints on textarea input", () => {
    const setEndpoints = vi.fn();
    render(<EtcdConfigSection {...makeProps({ setEndpoints })} />);
    const textarea = screen.getByPlaceholderText(/10\.0\.0\.1:2379/);
    fireEvent.change(textarea, { target: { value: "10.0.0.5:2379" } });
    expect(setEndpoints).toHaveBeenCalledWith("10.0.0.5:2379");
  });

  it("renders dial / command timeout numeric inputs with current values", () => {
    render(<EtcdConfigSection {...makeProps({ dialTimeoutSeconds: 7, commandTimeoutSeconds: 30 })} />);
    expect(screen.getByText("etcd.form.dialTimeout")).toBeInTheDocument();
    expect(screen.getByText("etcd.form.commandTimeout")).toBeInTheDocument();
    expect(screen.getByDisplayValue("7")).toBeInTheDocument();
    expect(screen.getByDisplayValue("30")).toBeInTheDocument();
  });

  it("calls setDialTimeoutSeconds when user types a number", () => {
    const setDialTimeoutSeconds = vi.fn();
    render(<EtcdConfigSection {...makeProps({ dialTimeoutSeconds: 5, setDialTimeoutSeconds })} />);
    const dialInput = screen.getByDisplayValue("5");
    fireEvent.change(dialInput, { target: { value: "12" } });
    expect(setDialTimeoutSeconds).toHaveBeenCalledWith(12);
  });
});
