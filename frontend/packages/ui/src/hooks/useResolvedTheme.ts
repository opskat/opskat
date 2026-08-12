import { useEffect, useState } from "react";

/**
 * 返回当前生效的主题（"dark" | "light"），跟随系统和用户设置实时变化。
 *
 * 真相是 <html> 上的 .dark class：应用的 ThemeProvider 负责写入（它已经把
 * "system" 解析成具体值并监听了系统偏好变化），这里只观察结果。因此本 hook
 * 不依赖任何 Provider，包内组件也能自行取到与应用一致的主题。
 */
export function useResolvedTheme(): "dark" | "light" {
  const [resolved, setResolved] = useState<"dark" | "light">(() =>
    document.documentElement.classList.contains("dark") ? "dark" : "light"
  );

  useEffect(() => {
    const observer = new MutationObserver(() =>
      setResolved(document.documentElement.classList.contains("dark") ? "dark" : "light")
    );
    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["class"],
    });
    return () => observer.disconnect();
  }, []);

  return resolved;
}
