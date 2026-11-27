import { useEffect } from "react";

// 与 index.html 里 #boot 的 opacity 过渡时长一致。
const BOOT_FADE_MS = 200;

/**
 * 启动加载界面（index.html 的 #boot）在 React 首次提交后淡出并移除。和 <App /> 并列挂载：
 * effect 在真实界面画出来之后才跑，淡出时底下已经是应用本身。
 */
export function DismissBootScreen() {
  useEffect(() => {
    const boot = document.getElementById("boot");
    if (!boot) return;
    boot.classList.add("boot-done");
    const timer = setTimeout(() => boot.remove(), BOOT_FADE_MS);
    return () => clearTimeout(timer);
  }, []);
  return null;
}
