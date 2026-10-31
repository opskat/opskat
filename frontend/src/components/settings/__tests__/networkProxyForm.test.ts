import { describe, it, expect } from "vitest";
import { validateNetworkProxyForm, toNetworkProxyInput, type NetworkProxyForm } from "../networkProxyForm";

const custom = (over: Partial<NetworkProxyForm> = {}): NetworkProxyForm => ({
  mode: "custom",
  type: "http",
  host: "127.0.0.1",
  port: "7890",
  username: "",
  password: "",
  noProxy: "localhost\n10.0.0.0/8",
  ...over,
});

describe("validateNetworkProxyForm", () => {
  it("accepts a complete custom form", () => {
    expect(validateNetworkProxyForm(custom())).toEqual({});
  });

  it("does not validate the custom part when another mode is selected", () => {
    expect(validateNetworkProxyForm(custom({ mode: "direct", host: "", port: "" }))).toEqual({});
    expect(validateNetworkProxyForm(custom({ mode: "env", host: "bad host" }))).toEqual({});
  });

  it.each([
    ["", "required"],
    ["http://127.0.0.1", "invalid"],
    ["proxy host", "invalid"],
    ["proxy.example.com/x", "invalid"],
    ["1.2.3", "invalid"],
    ["-bad.example.com", "invalid"],
  ])("rejects host %j as %s", (host, code) => {
    expect(validateNetworkProxyForm(custom({ host })).host).toBe(code);
  });

  it.each(["127.0.0.1", "proxy.example.com", "clash_box", "::1", "fe80::1", "2001:db8::2"])(
    "accepts host %s",
    (host) => {
      expect(validateNetworkProxyForm(custom({ host })).host).toBeUndefined();
    }
  );

  it.each([
    ["", "required"],
    ["0", "out_of_range"],
    ["65536", "out_of_range"],
    ["78900", "out_of_range"],
    ["78.5", "invalid"],
    ["abc", "invalid"],
  ])("rejects port %j as %s", (port, code) => {
    expect(validateNetworkProxyForm(custom({ port })).port).toBe(code);
  });

  it.each(["1", "7890", "65535"])("accepts port %s", (port) => {
    expect(validateNetworkProxyForm(custom({ port })).port).toBeUndefined();
  });

  it("reports the first unrecognised bypass line, counting blank lines", () => {
    const errors = validateNetworkProxyForm(custom({ noProxy: "localhost\n\nhttp://x.com\nbad host" }));
    expect(errors.noProxy).toEqual({ line: 3 });
  });

  it("accepts domain, .domain, *.domain, IP, CIDR and blank lines", () => {
    const noProxy = [
      "example.com",
      ".example.com",
      "*.example.com",
      "10.1.2.3",
      "::1",
      "192.168.0.0/16",
      "fd00::/8",
      "",
      "  ",
    ].join("\n");
    expect(validateNetworkProxyForm(custom({ noProxy })).noProxy).toBeUndefined();
  });

  it.each(["10.0.0.0/33", "fd00::/129", "10.0.0.0/x", "*.", "*", "a b.com", "1.2.3.256"])(
    "rejects bypass line %j",
    (noProxy) => {
      expect(validateNetworkProxyForm(custom({ noProxy })).noProxy).toEqual({ line: 1 });
    }
  );
});

describe("toNetworkProxyInput", () => {
  it("maps the form to the binding input with a numeric port", () => {
    expect(toNetworkProxyInput(custom({ type: "socks5", username: "ops", password: "pw" }))).toEqual({
      mode: "custom",
      type: "socks5",
      host: "127.0.0.1",
      port: 7890,
      username: "ops",
      password: "pw",
      noProxy: "localhost\n10.0.0.0/8",
    });
  });

  it("sends port 0 instead of NaN when the port is not a number outside custom mode", () => {
    expect(toNetworkProxyInput(custom({ mode: "direct", port: "" })).port).toBe(0);
  });
});
