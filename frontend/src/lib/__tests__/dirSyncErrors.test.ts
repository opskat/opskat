import { describe, expect, it } from "vitest";
import { DIRSYNC_ERROR_CODES, formatDirSyncError, getDirSyncErrorMessageKey } from "@/lib/dirSyncErrors";

// 后端以错误码作为错误文本返回；Wails 2.16 起它装在 Error 对象里 reject。
describe("dirSyncErrors with Wails Error rejections", () => {
  it("recognises the code carried by an Error rejection", () => {
    expect(getDirSyncErrorMessageKey(new Error(DIRSYNC_ERROR_CODES.TIMEOUT))).toBe("sftp.sync.timeout");
  });

  it("shows the backend text, not 'Error: …', for an unknown code", () => {
    expect(formatDirSyncError((k) => k, new Error("ssh: disconnected"))).toBe("ssh: disconnected");
  });
});
