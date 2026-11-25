import { toast } from "sonner";

// 成功类提示统一放到顶部居中：终端 / AI / 查询结果都是从下往上刷新，关键信息在底部，
// 提示落在底部（sonner 默认右下角）会遮挡正在刷新的输出（见 #135）。
// 错误 / 警告仍走 toast.error / toast.warning 留在右下角——报错文案长、需要停留细看，
// 顶部居中放不下太多内容。
const SUCCESS_POSITION = "top-center" as const;

// 复制 / 剪贴板确认：高频、扫一眼就行，1s 一闪而过即可。传 id 时覆盖同 id 的前一条提示，避免连续复制堆叠。
export function notifyCopied(message: string, options?: { id?: string }) {
  toast.success(message, {
    position: SUCCESS_POSITION,
    duration: 1000,
    ...(options?.id && { id: options.id }),
  });
}

// 操作成功确认：顶部居中，沿用 sonner 默认停留时长；action 给出可点击的后续入口（如打开新建的资源）。
export function notifySuccess(message: string, options?: { action?: { label: string; onClick: () => void } }) {
  toast.success(message, { position: SUCCESS_POSITION, ...(options?.action && { action: options.action }) });
}
