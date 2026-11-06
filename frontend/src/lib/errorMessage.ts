// 把捕获到的错误转成给人看的文本。Wails 2.16 起后端错误以 Error 对象 reject
// （2.12 是裸字符串），String(err) / `${err}` 会多出 "Error: " 前缀，也会让按原文
// 匹配错误码的逻辑失配 —— 展示或匹配捕获到的错误一律经过这里。
export function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
