import { describe, expect, it } from "vitest";
import type { TFunction } from "i18next";
import { parseReviewInfo, resolveInheritedPermissionMode, reviewSummary, reviewWithMode } from "../commandReview";

// 把插值参数一起输出，才能看出拼出来的是哪几段文案。
const t = ((key: string, opts?: Record<string, unknown>) =>
  opts ? `${key}(${Object.values(opts).join("|")})` : key) as unknown as TFunction;

describe("reviewWithMode", () => {
  it("在审核结果前写上触发审核的权限模式", () => {
    expect(reviewWithMode(t, { mode: "autopilot", outcome: "pass" })).toBe(
      "commandReview.withMode(commandReview.mode.autopilot|commandReview.summary.pass)"
    );
  });
});

describe("reviewSummary", () => {
  it("审核通过", () => {
    expect(reviewSummary(t, { mode: "assisted", outcome: "pass" })).toBe("commandReview.summary.pass");
  });

  it("审核未通过时列出没满足的题目", () => {
    expect(reviewSummary(t, { mode: "assisted", outcome: "reject", failed: ["disruptive", "remote_code"] })).toBe(
      "commandReview.summary.reject(commandReview.question.disruptive" +
        "commandReview.listSeparator" +
        "commandReview.question.remote_code)"
    );
  });

  it("审核失败时写明原因，未知原因按服务不可用显示", () => {
    expect(reviewSummary(t, { mode: "assisted", outcome: "fail", reason: "timeout" })).toBe(
      "commandReview.summary.fail(commandReview.reason.timeout)"
    );
    expect(reviewSummary(t, { mode: "assisted", outcome: "fail", reason: "undecodable" })).toBe(
      "commandReview.summary.fail(commandReview.reason.undecodable)"
    );
    expect(reviewSummary(t, { mode: "assisted", outcome: "fail", reason: "something_new" })).toBe(
      "commandReview.summary.fail(commandReview.reason.unavailable)"
    );
  });
});

describe("resolveInheritedPermissionMode", () => {
  const g = (ID: number, ParentID: number, permissionMode = "") => ({ ID, ParentID, Name: `g${ID}`, permissionMode });
  const none = { mode: "default", from: null };

  it("沿分组链向上找第一个设置了的，并给出来源分组", () => {
    const groups = [g(1, 0, "autopilot"), g(2, 1), g(3, 2)];
    expect(resolveInheritedPermissionMode(3, groups)).toEqual({ mode: "autopilot", from: groups[0] });
  });

  it("近的分组优先", () => {
    const groups = [g(1, 0, "autopilot"), g(2, 1, "default")];
    expect(resolveInheritedPermissionMode(2, groups)).toEqual({ mode: "default", from: groups[1] });
  });

  it("都没设置、没有分组或分组不存在时是默认，没有来源", () => {
    expect(resolveInheritedPermissionMode(2, [g(1, 0), g(2, 1)])).toEqual(none);
    expect(resolveInheritedPermissionMode(0, [g(1, 0, "autopilot")])).toEqual(none);
    expect(resolveInheritedPermissionMode(9, [g(1, 0, "autopilot")])).toEqual(none);
  });

  it("和后端一样最多向上找 5 层", () => {
    const groups = [g(1, 0, "autopilot"), g(2, 1), g(3, 2), g(4, 3), g(5, 4), g(6, 5)];
    expect(resolveInheritedPermissionMode(5, groups).mode).toBe("autopilot");
    expect(resolveInheritedPermissionMode(6, groups)).toEqual(none);
  });

  it("分组链成环时不死循环", () => {
    expect(resolveInheritedPermissionMode(1, [g(1, 2), g(2, 1)])).toEqual(none);
  });
});

describe("parseReviewInfo", () => {
  it("解析审计里存的审核结果", () => {
    expect(parseReviewInfo('{"outcome":"reject","failed":["disruptive"]}')).toEqual({
      outcome: "reject",
      failed: ["disruptive"],
    });
  });

  it("不是审核结果时返回 null，由调用方原样显示", () => {
    expect(parseReviewInfo("not json")).toBeNull();
    expect(parseReviewInfo('{"foo":1}')).toBeNull();
    expect(parseReviewInfo("null")).toBeNull();
  });
});
