import i18n from "i18next";
import { initReactI18next } from "react-i18next";

import zhCommon from "./locales/zh-CN/common.json";
import enCommon from "./locales/en/common.json";

const resources = {
  "zh-CN": { common: zhCommon },
  en: { common: enCommon },
};

i18n.use(initReactI18next).init({
  resources,
  lng: localStorage.getItem("language") || navigator.language || "zh-CN",
  fallbackLng: "zh-CN",
  defaultNS: "common",
  interpolation: {
    escapeValue: false,
  },
});

export default i18n;
