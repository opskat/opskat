import type { Plugin } from "vite";

// wails dev 把每个请求先转给 vite；vite 的 SPA 回退会对 /extensions/<name>/frontend/*
// 这类静态文件（Accept 含 */*）返回 index.html，扩展自己的 CSS/JS 永远拿不到。
// Wails 在外部 dev server 回 404/405 时才把请求交给 Go 的 AssetServer.Handler
// （ExtensionAssetHandler），所以这里在 vite 回退之前对 /extensions/ 直接回 404。
// 仅 vite serve 生效，生产构建不受影响。
export function extensionsPassthrough(): Plugin {
  return {
    name: "opskat:extensions-passthrough",
    apply: "serve",
    configureServer(server) {
      // 直接 use（不返回回调）= 注册在 vite 内置 SPA/HTML 回退之前
      server.middlewares.use((req, res, next) => {
        const pathname = (req.url ?? "").split("?")[0];
        if (!pathname.startsWith("/extensions/")) {
          next();
          return;
        }
        res.statusCode = 404;
        res.end();
      });
    },
  };
}
