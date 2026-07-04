import type { Components } from "react-markdown";
import { BrowserOpenURL } from "../../wailsjs/runtime/runtime";
import { normalizeHttpUrl } from "./terminal/terminalUrlScan";

// Wails 的 WebView 没有地址栏/后退按钮：点击 markdown 里的 <a href> 会用目标页面整页
// 替换掉应用 UI，用户再也回不来（#218）。所有 markdown 渲染统一挂这个 components——
// 拦截点击后只把 http/https 链接交给系统浏览器打开，与终端链接同一条路径
// （见 terminalRegistry 的 normalizeHttpUrl + BrowserOpenURL）。模块级常量，引用稳定，
// 不会让 <Markdown> 每次渲染都因为 components 换引用而重新解析。
export const markdownComponents: Components = {
  a({ href, children, node: _node, ...rest }) {
    return (
      <a
        {...rest}
        href={href}
        onClick={(event) => {
          event.preventDefault();
          const url = href ? normalizeHttpUrl(href) : undefined;
          if (url) BrowserOpenURL(url);
        }}
      >
        {children}
      </a>
    );
  },
};
