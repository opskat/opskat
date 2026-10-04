import i18n from "i18next";
import { initReactI18next } from "react-i18next";

import zhCommon from "./locales/zh-CN/common.json";
import enCommon from "./locales/en/common.json";
import { SetLanguage } from "../../wailsjs/go/system/System";

const resources = {
  "zh-CN": { common: zhCommon },
  en: { common: enCommon },
};

function detectLanguage(): string {
  const saved = localStorage.getItem("language");
  if (saved) return saved;
  return (navigator.language || "").toLowerCase().startsWith("zh") ? "zh-CN" : "en";
}

const initialLanguage = detectLanguage();

i18n.use(initReactI18next).init({
  resources,
  lng: initialLanguage,
  fallbackLng: "en",
  defaultNS: "common",
  interpolation: {
    escapeValue: false,
  },
});

// 唯一同步点：opsctl 审批弹窗（ext dev 安装详情、用户拒绝提示等）由 Go 后端拼出文案，
// 语言取自 System.Lang()，而后端自己没有别的办法知道用户选的 UI 语言——这里把它同步
// 过去一次覆盖启动时的默认值，并在每次切换时再同步一次，而不是要求每个调用
// i18n.changeLanguage 的地方（如 AppearanceSection）各自去调一次后端。
void SetLanguage(initialLanguage);
i18n.on("languageChanged", (lng) => {
  void SetLanguage(lng);
});

export default i18n;
