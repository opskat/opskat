import i18n from "i18next";
import { initReactI18next } from "react-i18next";

import { SetLanguage } from "../../wailsjs/go/system/System";
import zhCommon from "./locales/zh-CN/common.json";
import enCommon from "./locales/en/common.json";

const resources = {
  "zh-CN": { common: zhCommon },
  en: { common: enCommon },
};

function detectLanguage(): string {
  const saved = localStorage.getItem("language");
  if (saved) return saved;
  return (navigator.language || "").toLowerCase().startsWith("zh") ? "zh-CN" : "en";
}

i18n.use(initReactI18next).init({
  resources,
  lng: detectLanguage(),
  fallbackLng: "en",
  defaultNS: "common",
  interpolation: {
    escapeValue: false,
  },
});

// 后端按语言挑选文案（扩展名称、测试连接描述等），以界面语言为准：启动时同步一次，之后每次切换再同步。
export const backendLanguageReady: Promise<void> = SetLanguage(i18n.language);
i18n.on("languageChanged", (lng) => {
  SetLanguage(lng).catch((err) => console.error("Sync language to backend failed:", err));
});

export default i18n;
