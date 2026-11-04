import { describe, expect, it } from "vitest";
import { errorMessage } from "@/lib/errorMessage";

// Wails 2.16 起后端返回的错误以 Error 对象 reject（2.12 是裸字符串），
// String(err) 会多出 "Error: " 前缀；界面上展示的必须是后端给的原文。
describe("errorMessage", () => {
  it("returns the backend text of a Wails 2.16 rejection without the Error: prefix", () => {
    expect(errorMessage(new Error("redis 连接失败: dial tcp timeout"))).toBe("redis 连接失败: dial tcp timeout");
  });

  it("keeps a bare string rejection as is", () => {
    expect(errorMessage("会话不存在")).toBe("会话不存在");
  });

  it("stringifies anything else", () => {
    expect(errorMessage(42)).toBe("42");
  });
});
