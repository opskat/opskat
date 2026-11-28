import { useEffect } from "react";
import { useShortcutStore, eventMatchesBinding, matchShortcut, FIND_SCOPE_ATTR } from "@/stores/shortcutStore";
import { useLayoutStore } from "@/stores/layoutStore";
import { copySelectedAssetMarkdownRef, shouldCopyAssetRef } from "@/lib/assetRef";
import { dispatchAppAction, type AppShellActionHandlers } from "@/lib/appActionDispatch";

export function useKeyboardShortcuts({
  onToggleAIPanel,
  onToggleSidebar,
  onToggleCommandPalette,
}: AppShellActionHandlers) {
  useEffect(() => {
    const handlers: AppShellActionHandlers = {
      onToggleAIPanel,
      onToggleSidebar,
      onToggleCommandPalette,
    };

    const handler = (e: KeyboardEvent) => {
      const { shortcuts, isRecording, disableShortcutsInTerminal } = useShortcutStore.getState();
      if (isRecording) return;

      // 先计算 action：command.quickopen 需要在输入框内也能触发，
      // 必须早于"输入框内忽略"分支处理。
      const action = matchShortcut(e, shortcuts);
      const copyRefBinding = shortcuts["asset.copyRef"];
      if (copyRefBinding && eventMatchesBinding(e, copyRefBinding) && shouldCopyAssetRef(e.target)) {
        e.preventDefault();
        e.stopPropagation();
        void copySelectedAssetMarkdownRef();
        return;
      }
      // asset.* 只在资产侧栏聚焦时由侧栏处理（copyRef 已在上面按焦点判断过），全局不接管。
      if (!action || action.startsWith("asset.")) return;
      if (action.startsWith("terminal.")) return;
      // 表设计器的保存由设计器自己监听;这里不能 preventDefault,否则别的视图(如远程文件编辑器的 ⌘S)收不到。
      if (action === "designer.save") return;

      // command.quickopen 早期处理：在输入框内也应该能触发
      if (action === "command.quickopen") {
        e.preventDefault();
        e.stopPropagation();
        dispatchAppAction(action, handlers);
        return;
      }

      // 普通快捷键：输入框内（除 xterm）忽略
      // 注意 panel.filter 也走这一段，让出 Cmd+F 给浏览器/原生 find-in-page。
      const target = e.target as HTMLElement;

      // 「在终端内禁用快捷键」开启时：终端聚焦，把 app 快捷键透传给终端，
      // 让按键送达远端 shell。command.quickopen 已在上方提前处理，不受影响。
      if (disableShortcutsInTerminal && target.closest(".xterm")) return;

      if (
        (target.tagName === "INPUT" ||
          target.tagName === "TEXTAREA" ||
          target.tagName === "SELECT" ||
          target.isContentEditable) &&
        !target.closest(".xterm")
      ) {
        return;
      }

      // panel.filter 特殊处理：xterm / Monaco 编辑器 / 数据库结果表格聚焦时透传（各自打开查找），否则通过 store 触发 filter 面板。
      // Monaco 的焦点元素是 EditContext 的 div，上面的输入框判断拦不到，按 .monaco-editor 祖先识别。
      if (action === "panel.filter") {
        const el = document.activeElement as HTMLElement | null;
        if (el?.closest(`.xterm, .monaco-editor, [${FIND_SCOPE_ATTR}]`)) return;
        e.preventDefault();
        e.stopPropagation();
        useLayoutStore.getState().requestOpenFilter();
        return;
      }

      e.preventDefault();
      e.stopPropagation();
      dispatchAppAction(action, handlers);
    };

    window.addEventListener("keydown", handler, true);
    return () => window.removeEventListener("keydown", handler, true);
  }, [onToggleAIPanel, onToggleSidebar, onToggleCommandPalette]);
}
