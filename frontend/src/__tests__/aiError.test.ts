import { describe, it, expect, vi } from "vitest";

// classifyError 内部读取 i18n.t 取友好标题；t 按 key 查真实的 en 文案（缺失返回 undefined），
// 让「每个 kind 都有文案」能在 kind 与语言包脱节时失败（zh-CN 与 en 的 key 对齐由 i18n.test 保证）。
vi.mock("../i18n", async () => {
  const en = (await import("../i18n/locales/en/common.json")).default as Record<string, unknown>;
  return {
    default: {
      t: (key: string) =>
        key.split(".").reduce<unknown>((node, part) => (node as Record<string, unknown> | undefined)?.[part], en),
    },
  };
});

import { classifyError } from "../lib/aiError";

describe("classifyError", () => {
  it("命中 auth（401/403/api key/unauthorized）", () => {
    expect(classifyError("401 unauthorized").kind).toBe("auth");
    expect(classifyError("403 forbidden").kind).toBe("auth");
    expect(classifyError("invalid_api_key").kind).toBe("auth");
    expect(classifyError("authentication failed").kind).toBe("auth");
  });

  it("代理侧失败命中 proxy，且优先于 auth/network（代理认证失败不被说成 API Key 问题）", () => {
    expect(
      classifyError(
        'Post "https://api.deepseek.com/anthropic/v1/messages": socks connect tcp 127.0.0.1:18081->api.deepseek.com:443: username/password authentication failed'
      ).kind
    ).toBe("proxy");
    expect(classifyError('Post "https://huu.dqy.ink/v1/messages": Proxy Authentication Required').kind).toBe("proxy");
    expect(
      classifyError('Post "https://huu.dqy.ink/v1/messages": proxyconnect tcp: dial tcp 127.0.0.1:18080: i/o timeout')
        .kind
    ).toBe("proxy");
  });

  it("HTTP 代理拒绝 CONNECT（net/http 只报原因短语）命中 proxy，不被说成服务不可用", () => {
    expect(classifyError('Post "https://api.example.com/v1/messages": Bad Gateway').kind).toBe("proxy");
    expect(classifyError('Post "https://api.example.com/v1/messages": Service Unavailable').kind).toBe("proxy");
    expect(classifyError('Post "https://api.example.com/v1/messages": Forbidden').kind).toBe("proxy");
  });

  it("HTTP 代理拒绝 CONNECT 时原因短语缺失或全大写同样命中 proxy", () => {
    // 状态行 `HTTP/1.1 403`（无空格）→ net/http 报 unknown status code；`HTTP/1.1 403 `（空短语）→ 错误正文为空；
    // `HTTP/1.1 502 BAD GATEWAY` → 原样报全大写短语（不能被 server 的 bad gateway 捞走）
    expect(classifyError('Post "https://api.example.com/v1/messages": unknown status code').kind).toBe("proxy");
    expect(classifyError('Post "https://api.example.com/v1/messages": ').kind).toBe("proxy");
    expect(classifyError('Post "https://api.example.com/v1/messages": BAD GATEWAY').kind).toBe("proxy");
  });

  it("服务端自己的 5xx 与传输层 EOF 不被当成代理失败", () => {
    expect(classifyError('POST "https://api.example.com/v1/messages": 502 Bad Gateway {"error":"x"}').kind).toBe(
      "server"
    );
    expect(classifyError('Post "https://api.example.com/v1/messages": EOF').kind).toBe("network");
    expect(classifyError('Post "https://api.example.com/v1/messages": context deadline exceeded').kind).toBe("unknown");
  });

  it("命中 rate_limit（429/too many/rate limit）", () => {
    expect(classifyError("429 too many requests").kind).toBe("rate_limit");
    expect(classifyError("rate_limit_exceeded").kind).toBe("rate_limit");
  });

  it("命中 network（timeout/EOF/connection reset/i/o timeout/tls handshake）", () => {
    expect(classifyError("dial tcp: i/o timeout").kind).toBe("network");
    expect(classifyError("read: connection reset by peer").kind).toBe("network");
    expect(classifyError("EOF").kind).toBe("network");
    expect(classifyError("tls handshake timeout").kind).toBe("network");
  });

  it("命中 server（5xx/service unavailable/bad gateway）", () => {
    expect(classifyError("503 service unavailable").kind).toBe("server");
    expect(classifyError("502 bad gateway").kind).toBe("server");
    expect(classifyError("500 internal server error").kind).toBe("server");
  });

  it("auth 优先于 server（401 不被 5xx 误捞）", () => {
    expect(classifyError("provider returned 401: api key unauthorized").kind).toBe("auth");
  });

  it("rate_limit 优先于 server（429 不被 5xx 误捞）", () => {
    expect(classifyError("429 rate limit exceeded").kind).toBe("rate_limit");
  });

  it("空字符串与未知错误归类为 unknown", () => {
    expect(classifyError("").kind).toBe("unknown");
    expect(classifyError(null).kind).toBe("unknown");
    expect(classifyError("some weird message").kind).toBe("unknown");
  });

  it("显式 kind 跳过正则匹配（用于 interrupted 路径）", () => {
    expect(classifyError("401 unauthorized", "interrupted").kind).toBe("interrupted");
    expect(classifyError("429 too many", "interrupted").kind).toBe("interrupted");
  });

  it("message 字段在所有 kind 上非空", () => {
    const kinds = ["proxy", "rate_limit", "server", "network", "auth", "interrupted", "unknown"] as const;
    for (const k of kinds) {
      const { message } = classifyError("any", k);
      expect(message).toBeTruthy();
    }
  });
});
