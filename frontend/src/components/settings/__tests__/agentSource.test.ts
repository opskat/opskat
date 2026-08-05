import { describe, it, expect } from "vitest";
import { endpointDefaultValue, isEndpointStructurallyValid } from "@/components/settings/agentSource";

describe("agentSource endpoint helpers", () => {
  it("produces a structurally valid local pipe default", () => {
    const value = endpointDefaultValue("windows_named_pipe");
    expect(value).toBe("\\\\.\\pipe\\openssh-ssh-agent");
    expect(isEndpointStructurallyValid("windows_named_pipe", value)).toBe(true);
  });

  it("accepts local pipes but rejects remote UNC pipes", () => {
    expect(isEndpointStructurallyValid("windows_named_pipe", "\\\\.\\pipe\\openssh-ssh-agent")).toBe(true);
    expect(isEndpointStructurallyValid("windows_named_pipe", "\\\\server\\pipe\\ssh-agent")).toBe(false);
  });

  it("accepts env var name syntax and unix absolute/~ paths", () => {
    expect(isEndpointStructurallyValid("environment", "SSH_AUTH_SOCK")).toBe(true);
    expect(isEndpointStructurallyValid("environment", "1BAD NAME")).toBe(false);
    expect(isEndpointStructurallyValid("unix_socket", "/tmp/agent.sock")).toBe(true);
    expect(isEndpointStructurallyValid("unix_socket", "~/agent.sock")).toBe(true);
    expect(isEndpointStructurallyValid("unix_socket", "relative/agent.sock")).toBe(false);
  });
});
