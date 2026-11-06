import i18n, { type ResourceKey } from "i18next";
import { initReactI18next } from "react-i18next";

import { SetLanguage } from "../../wailsjs/go/system/System";

// 语言包按需加载：每个都是独立 chunk，启动只拉当前语言（main.tsx 等 i18nReady 再挂载，
// 首屏不会闪 key），另一种语言在切换时才拉。
const localeLoaders = {
  "zh-CN": () => import("./locales/zh-CN/common.json"),
  en: () => import("./locales/en/common.json"),
} satisfies Record<string, () => Promise<{ default: ResourceKey }>>;

type SupportedLanguage = keyof typeof localeLoaders;

function isSupportedLanguage(lng: string): lng is SupportedLanguage {
  return Object.prototype.hasOwnProperty.call(localeLoaders, lng);
}

function detectLanguage(): SupportedLanguage {
  const saved = localStorage.getItem("language");
  if (saved && isSupportedLanguage(saved)) return saved;
  return (navigator.language || "").toLowerCase().startsWith("zh") ? "zh-CN" : "en";
}

async function loadLocale(lng: string): Promise<void> {
  if (!isSupportedLanguage(lng) || i18n.hasResourceBundle(lng, "common")) return;
  const { default: common } = await localeLoaders[lng]();
  i18n.addResourceBundle(lng, "common", common);
}

const initialLanguage = detectLanguage();

// resources 给空表：i18next 同步完成初始化（store / 语言就位），语言包由下面按需补进来。
// 不用 i18next backend 插件：它会把 fallbackLng 一并加载，zh-CN 启动也得拉 en 包。
void i18n.use(initReactI18next).init({
  resources: {},
  lng: initialLanguage,
  fallbackLng: "en",
  defaultNS: "common",
  interpolation: {
    escapeValue: false,
  },
});

// 切换语言先拉到目标语言包再真正切换，界面不会出现一帧未翻译的 key；包加载失败则
// 保持当前语言并把错误交给调用方。重叠的切换以最后一次请求为准。
const applyLanguage = i18n.changeLanguage.bind(i18n);
let latestLanguageRequest = 0;
i18n.changeLanguage = async (lng, callback) => {
  const request = ++latestLanguageRequest;
  if (lng) await loadLocale(lng);
  if (request !== latestLanguageRequest) {
    callback?.(null, i18n.t);
    return i18n.t;
  }
  return applyLanguage(lng, callback);
};

/** 当前语言的语言包已就位；入口要等它再渲染。 */
export const i18nReady: Promise<void> = loadLocale(initialLanguage);

// 唯一同步点：opsctl 审批弹窗（ext dev 安装详情、用户拒绝提示等）由 Go 后端拼出文案，
// 语言取自 System.Lang()，而后端自己没有别的办法知道用户选的 UI 语言——这里把它同步
// 过去一次覆盖启动时的默认值，并在每次切换时再同步一次，而不是要求每个调用
// i18n.changeLanguage 的地方（如 AppearanceSection）各自去调一次后端。
void SetLanguage(initialLanguage);
i18n.on("languageChanged", (lng) => {
  void SetLanguage(lng);
});

export default i18n;
