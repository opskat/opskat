import { describe, expect, it } from "vitest";
import type { TFunction } from "i18next";
import { reviewSummary } from "../lib/commandReview";

// 文案键与插值原样写出来，看得出用的是哪句话、带了什么原因。
const t = ((key: string, opts?: Record<string, string>) =>
  opts ? `${key}(${Object.values(opts).join(",")})` : key) as unknown as TFunction;

// issue #384：没有送审的命令说明原因，不说成「审核失败」。
describe("没有送审的审核结果", () => {
  it.each(["piped_input", "policy_unparseable", "no_command", "secret_value", "unclassified", "undecodable"])(
    "%s",
    (reason) => {
      expect(reviewSummary(t, { mode: "assisted", outcome: "skipped", reason }, "zh-CN")).toBe(
        `commandReview.summary.skipped(commandReview.reason.${reason})`
      );
    }
  );

  it("审核失败照旧", () => {
    expect(reviewSummary(t, { mode: "autopilot", outcome: "fail", reason: "timeout" }, "en")).toBe(
      "commandReview.summary.fail(commandReview.reason.timeout)"
    );
  });
});
