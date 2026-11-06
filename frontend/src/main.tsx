import React from "react";
import { createRoot } from "react-dom/client";
import { i18nReady } from "./i18n";
import "./styles/globals.css";
import App from "./App";

// 只等当前语言包（一个本地 chunk）就位再挂载，首屏不会闪未翻译的 key；
// 其余数据加载在 App 内部异步进行。语言包加载失败也照常挂载——错误以 unhandled
// rejection 上报，界面退化为显示 key，而不是白屏。
const container = document.getElementById("root");
void i18nReady.finally(() => {
  createRoot(container!).render(
    <React.StrictMode>
      <App />
    </React.StrictMode>
  );
});
