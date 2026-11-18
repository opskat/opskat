package auto_backup_svc

import "errors"

// 给用户看的失败码。服务在后台运行、没有界面语言，状态又会持久化后再显示，
// 所以只记录与语言无关的失败码，由界面按当前语言翻译；Detail 是原始细节（路径、
// 服务器返回、系统错误），原样显示、不翻译。
const (
	ErrCodeFailed          = "failed"          // 其他失败：Detail 为原始错误
	ErrCodeFolderMissing   = "folder_missing"  // Detail：文件夹路径
	ErrCodeNotAFolder      = "not_a_folder"    // Detail：路径
	ErrCodeWriteFailed     = "write_failed"    // Detail：系统错误
	ErrCodeFolderRequired  = "folder_required" // 勾选了本地文件夹但没选路径
	ErrCodeGitHubSignedOut = "github_signed_out"
	ErrCodeExportFailed    = "export_failed"  // Detail：原始错误
	ErrCodeEncryptFailed   = "encrypt_failed" // Detail：原始错误
	ErrCodeDisabled        = "disabled"       // 自动备份未开启
)

// Error 是带失败码的错误。
type Error struct {
	Code   string
	Detail string
}

func (e *Error) Error() string {
	if e.Detail == "" {
		return "auto backup: " + e.Code
	}
	return "auto backup: " + e.Code + ": " + e.Detail
}

// describe 把错误拆成失败码与原始细节；不是 *Error 的错误归为通用失败，细节为原文。
func describe(err error) (code, detail string) {
	var e *Error
	if errors.As(err, &e) {
		return e.Code, e.Detail
	}
	return ErrCodeFailed, err.Error()
}
