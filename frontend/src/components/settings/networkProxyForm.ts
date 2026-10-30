import type { system } from "../../../wailsjs/go/models";

// 网络代理表单的值、校验与到后端入参的映射。校验规则与 internal/pkg/netproxy 的 Validate 保持一致：
// 后端是最终裁决，这里只是让用户在保存前就在字段下方看到原因。

export type NetworkProxyMode = "env" | "direct" | "custom";
export type NetworkProxyType = "http" | "socks5";

export interface NetworkProxyForm {
  mode: NetworkProxyMode;
  type: NetworkProxyType;
  host: string;
  /** 输入框原文；转成数字见 toNetworkProxyInput。 */
  port: string;
  username: string;
  /** 只在用户输入新密码时非空；留空表示保留已保存的密码。 */
  password: string;
  /** 每行一项。 */
  noProxy: string;
}

export type FieldCode = "required" | "invalid" | "out_of_range";

export interface NetworkProxyErrors {
  host?: FieldCode;
  port?: FieldCode;
  /** 第一个无法识别的行（从 1 开始，空行也计数）。 */
  noProxy?: { line: number };
}

const IPV4 = /^(0|[1-9]\d{0,2})(\.(0|[1-9]\d{0,2})){3}$/;

function isIPv4(s: string): boolean {
  return IPV4.test(s) && s.split(".").every((part) => Number(part) <= 255);
}

function isIPv6(s: string): boolean {
  if (!s.includes(":") || !/^[0-9A-Za-z:.]+$/.test(s)) return false;
  try {
    new URL(`http://[${s}]`);
    return true;
  } catch {
    return false;
  }
}

const isIP = (s: string) => isIPv4(s) || isIPv6(s);

const byteLength = (s: string) => new TextEncoder().encode(s).length;

// 主机名：以 "." 分隔的非空标签，每段由字母、数字、"_"、"-" 组成且不以 "-" 开头或结尾；末段不能全是数字。
function isHostname(host: string): boolean {
  if (byteLength(host) > 253) return false;
  const labels = host.split(".");
  for (const label of labels) {
    if (label === "" || byteLength(label) > 63 || label.startsWith("-") || label.endsWith("-")) return false;
    if (!/^[\p{L}\p{Nd}_-]+$/u.test(label)) return false;
  }
  return !/^\p{Nd}+$/u.test(labels[labels.length - 1]);
}

function isCIDR(s: string): boolean {
  const slash = s.indexOf("/");
  if (slash < 0) return false;
  const ip = s.slice(0, slash);
  const prefix = s.slice(slash + 1);
  if (!/^\d{1,3}$/.test(prefix)) return false;
  if (isIPv4(ip)) return Number(prefix) <= 32;
  return isIPv6(ip) && Number(prefix) <= 128;
}

// 「不走代理的地址」的一行：空行、IP、CIDR、域名（可带前导 "." 或 "*."）。
function isNoProxyLine(raw: string): boolean {
  const line = raw.trim();
  if (line === "" || isIP(line) || isCIDR(line)) return true;
  const domain = line.startsWith("*.") ? line.slice(2) : line.startsWith(".") ? line.slice(1) : line;
  return isHostname(domain);
}

function validateHost(host: string): FieldCode | undefined {
  if (host === "") return "required";
  return isIP(host) || isHostname(host) ? undefined : "invalid";
}

function validatePort(port: string): FieldCode | undefined {
  if (port === "") return "required";
  if (!/^\d+$/.test(port)) return "invalid";
  const n = Number(port);
  return n < 1 || n > 65535 ? "out_of_range" : undefined;
}

/** 返回有问题的字段；空对象表示可以保存。只有「自定义」模式才校验自定义部分。 */
export function validateNetworkProxyForm(form: NetworkProxyForm): NetworkProxyErrors {
  if (form.mode !== "custom") return {};
  const errors: NetworkProxyErrors = {};
  const host = validateHost(form.host);
  if (host) errors.host = host;
  const port = validatePort(form.port);
  if (port) errors.port = port;
  const bad = form.noProxy.split("\n").findIndex((line) => !isNoProxyLine(line));
  if (bad >= 0) errors.noProxy = { line: bad + 1 };
  return errors;
}

export const isNetworkProxyFormValid = (form: NetworkProxyForm): boolean =>
  Object.keys(validateNetworkProxyForm(form)).length === 0;

/** 表单 → 后端入参。非「自定义」模式下端口可能是空或乱填，转成 0，后端不校验也不会收到 NaN。 */
export function toNetworkProxyInput(form: NetworkProxyForm): system.NetworkProxyInput {
  return {
    mode: form.mode,
    type: form.type,
    host: form.host,
    port: /^\d+$/.test(form.port) ? Number(form.port) : 0,
    username: form.username,
    password: form.password,
    noProxy: form.noProxy,
  } as system.NetworkProxyInput;
}

/** 后端读取结果 → 表单；密码从不回显，始终为空。 */
export function networkProxyFormFromView(view: system.NetworkProxyView): NetworkProxyForm {
  return {
    mode: view.mode as NetworkProxyMode,
    type: view.type as NetworkProxyType,
    host: view.host,
    port: view.port === 0 ? "" : String(view.port),
    username: view.username,
    password: "",
    noProxy: view.noProxy,
  };
}
