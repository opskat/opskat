import { render, screen } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import { DatabaseConfigSection, type DatabaseConfigSectionProps } from "../DatabaseConfigSection";

function makeProps(overrides: Partial<DatabaseConfigSectionProps> = {}): DatabaseConfigSectionProps {
  return {
    host: "",
    setHost: vi.fn(),
    port: 0,
    setPort: vi.fn(),
    username: "",
    setUsername: vi.fn(),
    driver: "mysql",
    database: "",
    setDatabase: vi.fn(),
    sslMode: "disable",
    setSslMode: vi.fn(),
    tls: false,
    setTls: vi.fn(),
    readOnly: false,
    setReadOnly: vi.fn(),
    sshTunnelId: 0,
    setSshTunnelId: vi.fn(),
    params: "",
    setParams: vi.fn(),
    password: "",
    setPassword: vi.fn(),
    encryptedPassword: "",
    passwordSource: "inline",
    setPasswordSource: vi.fn(),
    passwordCredentialId: 0,
    setPasswordCredentialId: vi.fn(),
    managedPasswords: [],
    path: "",
    setPath: vi.fn(),
    ...overrides,
  };
}

describe("DatabaseConfigSection", () => {
  it("driver=sqlite 渲染 path 字段且不渲染 host", () => {
    render(<DatabaseConfigSection {...makeProps({ driver: "sqlite", path: "/tmp/x.db" })} />);
    expect(screen.getByDisplayValue("/tmp/x.db")).toBeInTheDocument();
    expect(screen.queryByPlaceholderText("example.com")).not.toBeInTheDocument();
  });

  it("driver=mssql 渲染 host + TLS 开关", () => {
    render(<DatabaseConfigSection {...makeProps({ driver: "mssql" })} />);
    expect(screen.getByPlaceholderText("example.com")).toBeInTheDocument();
    expect(screen.getByText("TLS")).toBeInTheDocument();
  });

  it("driver=mssql 默认端口 placeholder 是 1433", () => {
    render(<DatabaseConfigSection {...makeProps({ driver: "mssql" })} />);
    expect(screen.getByPlaceholderText("1433")).toBeInTheDocument();
  });
});
