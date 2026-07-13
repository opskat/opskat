import { createContext, use } from "react";

const CompactContext = createContext(false);

// 单独导出消息紧凑态上下文，避免 AIChatContent 文件同时导出组件和辅助 hook。
export function useCompact() {
  return use(CompactContext);
}

export { CompactContext };
