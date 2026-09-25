// opsctl 审批弹窗（ext dev 安装详情、用户拒绝提示等）由桌面端 Go 后端拼出文案，
// 语言取自 System.Lang()——它只在收到前端 SetLanguage 调用时才更新，默认值固定，
// 从不跟随系统/CLI locale。这份测试保护"前端 UI 语言是这条链路唯一可信源"这件事：
// 启动时把已探测的语言同步一次，之后每次切换都再同步一次，且只在这一个地方做，
// 而不是要求每个调用 i18n.changeLanguage 的调用点自己去调后端。
import { beforeEach, describe, expect, it, vi } from "vitest";

describe("frontend UI language syncs to the opsctl-approval backend", () => {
  beforeEach(() => {
    vi.resetModules();
    localStorage.clear();
  });

  it("pushes the detected language to the backend once at startup", async () => {
    localStorage.setItem("language", "en");
    const { SetLanguage } = await import("../../wailsjs/go/system/System");

    await import("@/i18n");

    expect(SetLanguage).toHaveBeenCalledWith("en");
  });

  it("pushes the backend language again whenever the UI language changes later", async () => {
    localStorage.setItem("language", "en");
    const { SetLanguage } = await import("../../wailsjs/go/system/System");
    const i18n = (await import("@/i18n")).default;
    vi.mocked(SetLanguage).mockClear();

    await i18n.changeLanguage("zh-CN");

    expect(SetLanguage).toHaveBeenCalledWith("zh-CN");
  });
});
