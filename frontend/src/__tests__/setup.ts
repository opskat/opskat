import { vi, afterEach } from "vitest";
import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";

// RTL does not auto-register cleanup when vitest globals are disabled.
// Register it explicitly so each test renders in isolation.
afterEach(() => cleanup());

// Mock Wails runtime
vi.mock("../../wailsjs/runtime/runtime", () => ({
  EventsOn: vi.fn(),
  EventsOff: vi.fn(),
  EventsEmit: vi.fn(),
  OnFileDrop: vi.fn(),
  OnFileDropOff: vi.fn(),
  BrowserOpenURL: vi.fn(),
  Quit: vi.fn(),
  WindowIsFullscreen: vi.fn().mockResolvedValue(false),
}));

// Mock Wails backend bindings — one factory per binder package.
// mockResolvedValue(undefined) 让所有 binding 默认返回 Promise<undefined>，
// 与真实 Wails binding 的签名一致，避免 `.catch(() => {})` 在 undefined 上报错。
async function mockBinderModule(modulePath: string) {
  const actual = await vi.importActual<Record<string, unknown>>(modulePath);
  const mocked: Record<string, unknown> = {};
  for (const key of Object.keys(actual)) {
    mocked[key] = vi.fn().mockResolvedValue(undefined);
  }
  return mocked;
}
vi.mock("../../wailsjs/go/system/System", () => mockBinderModule("../../wailsjs/go/system/System"));
vi.mock("../../wailsjs/go/ssh/SSH", () => mockBinderModule("../../wailsjs/go/ssh/SSH"));
vi.mock("../../wailsjs/go/query/Query", () => mockBinderModule("../../wailsjs/go/query/Query"));
vi.mock("../../wailsjs/go/redis/Redis", () => mockBinderModule("../../wailsjs/go/redis/Redis"));
vi.mock("../../wailsjs/go/kafka/Kafka", () => mockBinderModule("../../wailsjs/go/kafka/Kafka"));
vi.mock("../../wailsjs/go/etcd/Etcd", () => mockBinderModule("../../wailsjs/go/etcd/Etcd"));
vi.mock("../../wailsjs/go/k8s/K8s", () => mockBinderModule("../../wailsjs/go/k8s/K8s"));
vi.mock("../../wailsjs/go/serial/Serial", () => mockBinderModule("../../wailsjs/go/serial/Serial"));
vi.mock("../../wailsjs/go/ai/AI", () => mockBinderModule("../../wailsjs/go/ai/AI"));
vi.mock("../../wailsjs/go/opsctl/Opsctl", () => mockBinderModule("../../wailsjs/go/opsctl/Opsctl"));
vi.mock("../../wailsjs/go/extension/Extension", () => mockBinderModule("../../wailsjs/go/extension/Extension"));
vi.mock("../../wailsjs/go/external_edit/ExternalEdit", () =>
  mockBinderModule("../../wailsjs/go/external_edit/ExternalEdit")
);

// Mock react-i18next
vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string) => key,
    i18n: { language: "en", changeLanguage: vi.fn() },
  }),
  Trans: ({ i18nKey, children }: { i18nKey?: string; children?: React.ReactNode }) => i18nKey ?? children,
  initReactI18next: { type: "3rdParty", init: vi.fn() },
}));

// happy-dom 不做 layout —— Element.getBoundingClientRect() 一律 0 ×,IntersectionObserver
// / ResizeObserver 也不上报真实尺寸。@tanstack/react-virtual 用这两条决定渲染哪些行,
// 拿到 0 就一行都不渲染,QueryResultTable 的所有测试因此找不到任何 <tr>。
// 这里给出一个固定可视区尺寸,让虚拟化在测试里始终把"全部行"视为可见。
const TEST_CONTAINER_HEIGHT = 4000;
const TEST_CONTAINER_WIDTH = 1200;
class TestResizeObserver {
  private callback: ResizeObserverCallback;
  constructor(callback: ResizeObserverCallback) {
    this.callback = callback;
  }
  observe(target: Element) {
    const rect = {
      x: 0,
      y: 0,
      top: 0,
      left: 0,
      right: TEST_CONTAINER_WIDTH,
      bottom: TEST_CONTAINER_HEIGHT,
      width: TEST_CONTAINER_WIDTH,
      height: TEST_CONTAINER_HEIGHT,
      toJSON() {
        return this;
      },
    } as DOMRectReadOnly;
    const box = [{ inlineSize: TEST_CONTAINER_WIDTH, blockSize: TEST_CONTAINER_HEIGHT }];
    this.callback(
      [
        {
          target,
          contentRect: rect,
          borderBoxSize: box,
          contentBoxSize: box,
          devicePixelContentBoxSize: box,
        } as ResizeObserverEntry,
      ],
      this as unknown as ResizeObserver
    );
  }
  unobserve() {}
  disconnect() {}
}
vi.stubGlobal("ResizeObserver", TestResizeObserver);
Element.prototype.getBoundingClientRect = function () {
  return {
    x: 0,
    y: 0,
    top: 0,
    left: 0,
    right: TEST_CONTAINER_WIDTH,
    bottom: TEST_CONTAINER_HEIGHT,
    width: TEST_CONTAINER_WIDTH,
    height: TEST_CONTAINER_HEIGHT,
    toJSON() {
      return this;
    },
  } as DOMRect;
};

// Mock localStorage
const store: Record<string, string> = {};
vi.stubGlobal("localStorage", {
  getItem: (key: string) => store[key] ?? null,
  setItem: (key: string, val: string) => {
    store[key] = val;
  },
  removeItem: (key: string) => {
    delete store[key];
  },
  clear: () => {
    for (const key of Object.keys(store)) delete store[key];
  },
});
