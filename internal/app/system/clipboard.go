package system

// ReadClipboardText 读取本机剪贴板文本；剪贴板里没有文本（空、只有图片等）时返回 ""。
// 前端读剪贴板一律走这里，不直接调 Wails runtime 的 ClipboardGetText：后者在 Windows 上
// 会把"没有文本"报成错误码为 0 的错误（"The operation completed successfully"，#367），
// 超长文本还会被截断。写剪贴板仍用 Wails 的 ClipboardSetText。
func (s *System) ReadClipboardText() (string, error) {
	return readClipboardText(s.ctx)
}
