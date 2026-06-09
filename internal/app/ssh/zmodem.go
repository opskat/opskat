package ssh

import (
	"encoding/base64"
	"fmt"
	"path/filepath"

	"github.com/opskat/opskat/internal/pkg/transfer"

	"github.com/cago-frame/cago/pkg/logger"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"go.uber.org/zap"
)

// --- ZMODEM(lrzsz) 文件传输 ---
//
// 协议跑在前端 zmodem.js，这里只做两件事：弹原生对话框拿本地路径、把分块读写转发给
// zmodem_svc.FileBridge。进度由 FileBridge 经注入的 emit 发到 "transfer:progress:<id>"，
// 与 SFTP 复用同一前端订阅管线。所有跨边界的字节都按 base64 传，与 WriteSSH 一致。

// ZmodemUploadFile 是一个待上传文件的句柄信息，回传给前端逐个 send_offer。
type ZmodemUploadFile struct {
	TransferID string `json:"transferId"`
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	Mtime      int64  `json:"mtime"` // Unix 秒
}

// ZmodemChunk 是一次上传读块的结果：base64 数据 + 是否已读到文件尾。
type ZmodemChunk struct {
	Data string `json:"data"` // base64
	EOF  bool   `json:"eof"`
}

// ZmodemBeginDownload 弹原生 Save 对话框并创建本地文件，返回 transferID。
// 用户取消对话框时返回空字符串（前端据此 offer.skip()）。
func (s *SSH) ZmodemBeginDownload(sessionID, suggestedName string, size int64) (string, error) {
	localPath, err := wailsRuntime.SaveFileDialog(s.ctx, wailsRuntime.SaveDialogOptions{
		DefaultFilename: suggestedName,
		Title:           "保存接收的文件",
	})
	if err != nil {
		return "", fmt.Errorf("保存文件对话框失败: %w", err)
	}
	if localPath == "" {
		return "", nil // 用户取消
	}

	transferID := transfer.GenerateID("zmodem")
	if err := s.zmodem.BeginDownload(transferID, localPath, size); err != nil {
		return "", err
	}
	logger.Default().Info("zmodem download begin",
		zap.String("sessionID", sessionID), zap.String("transferID", transferID),
		zap.String("path", localPath), zap.Int64("size", size))
	return transferID, nil
}

// ZmodemAppendChunk 把一段已接收的 base64 字节追加写入下载文件。
func (s *SSH) ZmodemAppendChunk(transferID, dataB64 string) error {
	data, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return fmt.Errorf("解码下载分块失败: %w", err)
	}
	return s.zmodem.AppendChunk(transferID, data)
}

// ZmodemFinishDownload 关闭下载文件，置 done。
func (s *SSH) ZmodemFinishDownload(transferID string) error {
	logger.Default().Info("zmodem download finish", zap.String("transferID", transferID))
	return s.zmodem.FinishDownload(transferID)
}

// ZmodemAbortDownload 取消下载：删除半截文件，置为已取消状态。
func (s *SSH) ZmodemAbortDownload(transferID string) error {
	logger.Default().Info("zmodem download abort", zap.String("transferID", transferID))
	return s.zmodem.AbortDownload(transferID)
}

// ZmodemPickUploadFiles 弹原生多选对话框，逐个打开并登记上传句柄，返回文件列表。
// 用户取消时返回空列表（前端据此 session.close()）。
func (s *SSH) ZmodemPickUploadFiles(sessionID string) ([]ZmodemUploadFile, error) {
	paths, err := wailsRuntime.OpenMultipleFilesDialog(s.ctx, wailsRuntime.OpenDialogOptions{
		Title: "选择上传文件",
	})
	if err != nil {
		return nil, fmt.Errorf("打开文件对话框失败: %w", err)
	}

	files := make([]ZmodemUploadFile, 0, len(paths))
	for _, p := range paths {
		transferID := transfer.GenerateID("zmodem")
		size, mtime, openErr := s.zmodem.OpenUpload(transferID, p)
		if openErr != nil {
			logger.Default().Warn("zmodem open upload file", zap.String("path", p), zap.Error(openErr))
			continue
		}
		files = append(files, ZmodemUploadFile{
			TransferID: transferID,
			Name:       filepath.Base(p),
			Size:       size,
			Mtime:      mtime,
		})
	}
	logger.Default().Info("zmodem upload pick",
		zap.String("sessionID", sessionID), zap.Int("count", len(files)))
	return files, nil
}

// ZmodemReadChunk 读取至多 n 字节的上传内容，base64 返回，并标记是否读到文件尾。
func (s *SSH) ZmodemReadChunk(transferID string, n int) (ZmodemChunk, error) {
	data, eof, err := s.zmodem.ReadChunk(transferID, n)
	if err != nil {
		return ZmodemChunk{}, err
	}
	return ZmodemChunk{Data: base64.StdEncoding.EncodeToString(data), EOF: eof}, nil
}

// ZmodemFinishUpload 关闭上传读句柄，置 done。
func (s *SSH) ZmodemFinishUpload(transferID string) error {
	logger.Default().Info("zmodem upload finish", zap.String("transferID", transferID))
	return s.zmodem.FinishUpload(transferID)
}

// ZmodemAbortUpload 取消上传：关闭读句柄（不删本地源文件），置为已取消状态。
func (s *SSH) ZmodemAbortUpload(transferID string) error {
	logger.Default().Info("zmodem upload abort", zap.String("transferID", transferID))
	return s.zmodem.AbortUpload(transferID)
}
