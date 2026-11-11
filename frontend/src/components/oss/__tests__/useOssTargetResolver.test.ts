import { describe, expect, it } from "vitest";
import { keepBothKey } from "../useOssTargetResolver";

// 与 internal/service/sftp_svc/file_ops.go 的 uniqueRemotePath 同一规则：扩展名取 path.Ext(base)。
describe("keepBothKey", () => {
  it.each([
    ["dir/report.txt", 0, "dir/report_copy.txt"],
    ["dir/report.txt", 1, "dir/report_copy2.txt"],
    ["archive.tar.gz", 0, "archive.tar_copy.gz"],
    ["noext", 0, "noext_copy"],
    // path.Ext(".env") === ".env"，所以 SFTP 得到 _copy.env；OSS 必须一致。
    ["dir/.env", 0, "dir/_copy.env"],
    ["a.", 0, "a_copy."],
  ])("%s (attempt %i) -> %s", (key, attempt, expected) => {
    expect(keepBothKey(key, attempt)).toBe(expected);
  });
});
