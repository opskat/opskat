import { describe, expect, it, vi } from "vitest";
import { extensionsPassthrough } from "../../vite-plugins/extensionsPassthrough";

type Middleware = (req: { url?: string }, res: { statusCode: number; end: () => void }, next: () => void) => void;

function run(url: string) {
  const plugin = extensionsPassthrough();
  let middleware!: Middleware;
  (plugin.configureServer as (s: unknown) => void)({
    middlewares: { use: (fn: Middleware) => (middleware = fn) },
  });
  const res = { statusCode: 200, end: vi.fn() };
  const next = vi.fn();
  middleware({ url }, res, next);
  return { res, next };
}

describe("extensionsPassthrough (vite dev)", () => {
  it("answers 404 for extension static files so wails falls back to the Go asset handler", () => {
    const { res, next } = run("/extensions/x/frontend/style.css?t=1");
    expect(res.statusCode).toBe(404);
    expect(res.end).toHaveBeenCalled();
    expect(next).not.toHaveBeenCalled();
  });

  it("leaves other paths to vite", () => {
    for (const url of ["/", "/src/main.tsx", "/extensionsfoo/a.css", "/assets/extensions/a.css"]) {
      const { res, next } = run(url);
      expect(next).toHaveBeenCalledTimes(1);
      expect(res.end).not.toHaveBeenCalled();
    }
  });

  it("only applies to the dev server, not the production build", () => {
    expect(extensionsPassthrough().apply).toBe("serve");
  });
});
