// 语言包按需加载：启动只拉当前语言，切换时先把目标语言拉到再切，切换失败不留半截状态。
// 这里要看真实的翻译文本，所以撤掉 setup.ts 里全局的 react-i18next mock。
import { createElement } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, render, screen } from "@testing-library/react";

vi.unmock("react-i18next");

async function loadI18n() {
  const mod = await import("@/i18n");
  await mod.i18nReady;
  return mod.default;
}

async function SaveLabel() {
  const { useTranslation } = await import("react-i18next");
  return function Label() {
    const { t } = useTranslation();
    return createElement("span", { "data-testid": "label" }, t("action.save"));
  };
}

describe("i18n locale bundles load on demand", () => {
  beforeEach(() => {
    vi.resetModules();
    localStorage.clear();
  });

  afterEach(() => {
    vi.doUnmock("@/i18n/locales/zh-CN/common.json");
    vi.restoreAllMocks();
  });

  it("starts with only the active locale loaded", async () => {
    localStorage.setItem("language", "en");
    const i18n = await loadI18n();

    expect(i18n.language).toBe("en");
    expect(i18n.hasResourceBundle("en", "common")).toBe(true);
    expect(i18n.hasResourceBundle("zh-CN", "common")).toBe(false);
    expect(i18n.t("action.save")).toBe("Save");
  });

  it("loads the other locale on switch and renders its text", async () => {
    localStorage.setItem("language", "en");
    const i18n = await loadI18n();
    const Label = await SaveLabel();
    render(createElement(Label));
    expect(screen.getByTestId("label")).toHaveTextContent("Save");

    await act(async () => {
      await i18n.changeLanguage("zh-CN");
    });

    expect(i18n.hasResourceBundle("zh-CN", "common")).toBe(true);
    expect(screen.getByTestId("label")).toHaveTextContent("保存");
  });

  it("resolves an unsupported stored language to a supported one from the system locale", async () => {
    localStorage.setItem("language", "fr");
    vi.spyOn(navigator, "language", "get").mockReturnValue("zh-TW");
    const i18n = await loadI18n();

    expect(i18n.language).toBe("zh-CN");
    expect(i18n.t("action.save")).toBe("保存");
  });

  it("applies the last requested language when switches overlap", async () => {
    localStorage.setItem("language", "en");
    const i18n = await loadI18n();

    await act(async () => {
      const toZh = i18n.changeLanguage("zh-CN");
      const backToEn = i18n.changeLanguage("en");
      await Promise.all([toZh, backToEn]);
    });

    expect(i18n.language).toBe("en");
    expect(i18n.t("action.save")).toBe("Save");
  });

  it("keeps the current language when the target locale fails to load", async () => {
    localStorage.setItem("language", "en");
    vi.doMock("@/i18n/locales/zh-CN/common.json", () => {
      throw new Error("chunk load failed");
    });
    const i18n = await loadI18n();

    // vitest 把工厂抛出的错误包成自己的 "error when mocking a module"，原始错误挂在 cause 上。
    await expect(i18n.changeLanguage("zh-CN")).rejects.toMatchObject({ cause: { message: "chunk load failed" } });
    expect(i18n.language).toBe("en");
    expect(i18n.t("action.save")).toBe("Save");
  });
});
