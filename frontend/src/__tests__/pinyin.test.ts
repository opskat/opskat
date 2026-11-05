import { describe, it, expect, beforeAll, vi } from "vitest";
import { pinyin } from "pinyin-pro";
import { pinyinMatch } from "../lib/pinyin";

vi.mock("pinyin-pro", async (importOriginal) => {
  const actual = await importOriginal<typeof import("pinyin-pro")>();
  return { ...actual, pinyin: vi.fn(actual.pinyin) };
});

describe("pinyinMatch", () => {
  // The dictionary loads asynchronously at module load; wait until pinyin matching is live.
  beforeAll(() => vi.waitFor(() => expect(pinyinMatch("拼音", "py")).toBe(true)));

  it("computes a string's pinyin once and reuses it for later queries", () => {
    const text = "缓存测试主机";
    const calls = () => vi.mocked(pinyin).mock.calls.filter(([s]) => s === text).length;
    expect(pinyinMatch(text, "hc")).toBe(true);
    const afterFirst = calls();
    expect(afterFirst).toBeGreaterThan(0);
    expect(pinyinMatch(text, "zhuji")).toBe(true);
    expect(pinyinMatch(text, "hcceshi")).toBe(true);
    expect(pinyinMatch(text, "xyz")).toBe(false);
    expect(calls()).toBe(afterFirst);
  });

  it("returns true for empty query", () => {
    expect(pinyinMatch("anything", "")).toBe(true);
  });

  it("matches original text (case insensitive)", () => {
    expect(pinyinMatch("Hello World", "hello")).toBe(true);
    expect(pinyinMatch("Hello World", "world")).toBe(true);
    expect(pinyinMatch("Hello World", "xyz")).toBe(false);
  });

  it("matches Chinese characters directly", () => {
    expect(pinyinMatch("中转站", "中转")).toBe(true);
    expect(pinyinMatch("中转站", "站")).toBe(true);
  });

  it("matches by full pinyin", () => {
    expect(pinyinMatch("中转站", "zhongzhuanzhan")).toBe(true);
    expect(pinyinMatch("中转站", "zhongzhuan")).toBe(true);
  });

  it("matches by initials only", () => {
    expect(pinyinMatch("中转站", "zzz")).toBe(true);
    expect(pinyinMatch("中转站", "zz")).toBe(true);
  });

  it("matches by mixed pinyin (full + initials)", () => {
    expect(pinyinMatch("中转站", "zhongzz")).toBe(true);
    expect(pinyinMatch("中转站", "zhuanz")).toBe(true);
  });

  it("returns false for non-matching query", () => {
    expect(pinyinMatch("中转站", "abc")).toBe(false);
    expect(pinyinMatch("中转站", "xxx")).toBe(false);
  });

  it("handles mixed Chinese and English text", () => {
    expect(pinyinMatch("Web服务器", "web")).toBe(true);
    expect(pinyinMatch("Web服务器", "fwq")).toBe(true);
    expect(pinyinMatch("Web服务器", "fuwuqi")).toBe(true);
  });
});
