import { describe, it, expect, vi, beforeEach } from "vitest";

import i18next from "i18next";

import { SetLanguage } from "../../wailsjs/go/system/System";

describe("i18n backend language sync", () => {
  beforeEach(() => {
    // i18next 是外部依赖单例，不随 resetModules 重置；清掉上一个用例导入模块留下的监听
    i18next.off("languageChanged");
    vi.resetModules();
    vi.mocked(SetLanguage).mockClear();
    localStorage.setItem("language", "en");
  });

  it("syncs the initial language to the backend and resolves once it is applied", async () => {
    const { backendLanguageReady } = await import("../i18n");
    await backendLanguageReady;
    expect(SetLanguage).toHaveBeenCalledTimes(1);
    expect(SetLanguage).toHaveBeenCalledWith("en");
  });

  it("syncs again on every language change", async () => {
    const { default: i18n, backendLanguageReady } = await import("../i18n");
    await backendLanguageReady;
    await i18n.changeLanguage("zh-CN");
    expect(SetLanguage).toHaveBeenCalledTimes(2);
    expect(SetLanguage).toHaveBeenLastCalledWith("zh-CN");
  });
});
