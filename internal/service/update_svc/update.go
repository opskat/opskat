package update_svc

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/cago-frame/cago/configs"
)

const (
	githubRepo   = "CodFrm/ops-cat"
	apiLatestURL = "https://api.github.com/repos/" + githubRepo + "/releases/latest"
)

// ReleaseAsset GitHub release 资产
type ReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// ReleaseInfo GitHub release 信息
type ReleaseInfo struct {
	TagName     string         `json:"tag_name"`
	Name        string         `json:"name"`
	Body        string         `json:"body"`
	HTMLURL     string         `json:"html_url"`
	PublishedAt string         `json:"published_at"`
	Assets      []ReleaseAsset `json:"assets"`
}

// UpdateInfo 更新检查结果
type UpdateInfo struct {
	HasUpdate      bool   `json:"hasUpdate"`
	CurrentVersion string `json:"currentVersion"`
	LatestVersion  string `json:"latestVersion"`
	ReleaseNotes   string `json:"releaseNotes"`
	ReleaseURL     string `json:"releaseURL"`
	PublishedAt    string `json:"publishedAt"`
}

// CheckForUpdate 检查 GitHub 最新 release
func CheckForUpdate() (*UpdateInfo, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("GET", apiLatestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req) //nolint:gosec // request to constant GitHub API URL
	if err != nil {
		return nil, fmt.Errorf("request GitHub API failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var release ReleaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("decode response failed: %w", err)
	}

	currentVersion := configs.Version
	latestVersion := release.TagName

	info := &UpdateInfo{
		CurrentVersion: currentVersion,
		LatestVersion:  latestVersion,
		ReleaseNotes:   release.Body,
		ReleaseURL:     release.HTMLURL,
		PublishedAt:    release.PublishedAt,
	}

	// 比较版本: 去掉 v 前缀后直接字符串比较
	// 如果当前是 dev 版本，始终认为有更新
	if currentVersion == "dev" || currentVersion == "" {
		info.HasUpdate = true
	} else {
		cv := strings.TrimPrefix(currentVersion, "v")
		lv := strings.TrimPrefix(latestVersion, "v")
		info.HasUpdate = lv != cv && compareVersions(lv, cv) > 0
	}

	return info, nil
}

// DownloadAndUpdate 下载最新版本并替换当前二进制
// onProgress 回调参数: 已下载字节数, 总字节数
func DownloadAndUpdate(onProgress func(downloaded, total int64)) error {
	// 获取最新 release 信息
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("GET", apiLatestURL, nil)
	if err != nil {
		return fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req) //nolint:gosec // request to constant GitHub API URL
	if err != nil {
		return fmt.Errorf("request GitHub API failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var release ReleaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return fmt.Errorf("decode response failed: %w", err)
	}

	// 找到当前平台的桌面端资产
	platform := runtime.GOOS + "-" + runtime.GOARCH
	assetName := fmt.Sprintf("ops-cat-%s", platform)

	var downloadURL string
	var assetSize int64
	for _, asset := range release.Assets {
		if strings.HasPrefix(asset.Name, assetName) {
			downloadURL = asset.BrowserDownloadURL
			assetSize = asset.Size
			break
		}
	}
	if downloadURL == "" {
		return fmt.Errorf("no release asset found for platform %s", platform)
	}

	// 下载资产
	dlClient := &http.Client{Timeout: 30 * time.Minute}
	dlResp, err := dlClient.Get(downloadURL)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer func() { _ = dlResp.Body.Close() }()

	if dlResp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned status %d", dlResp.StatusCode)
	}

	if assetSize == 0 {
		assetSize = dlResp.ContentLength
	}

	// 下载到临时文件
	tmpFile, err := os.CreateTemp("", "ops-cat-update-*")
	if err != nil {
		return fmt.Errorf("create temp file failed: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	var reader io.Reader = dlResp.Body
	if onProgress != nil {
		reader = &progressReader{r: dlResp.Body, total: assetSize, onProgress: onProgress}
	}

	if _, err := io.Copy(tmpFile, reader); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("download write failed: %w", err)
	}
	_ = tmpFile.Close()

	// 获取当前可执行文件路径
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path failed: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("resolve executable path failed: %w", err)
	}

	// 解压并替换
	switch runtime.GOOS {
	case "darwin":
		return updateMacOS(tmpPath, execPath)
	case "windows":
		return updateWindows(tmpPath, execPath)
	default:
		return updateLinux(tmpPath, execPath)
	}
}

// updateMacOS 更新 macOS .app bundle
func updateMacOS(archivePath, execPath string) error {
	// execPath 类似 /path/to/ops-cat.app/Contents/MacOS/ops-cat
	// 需要找到 .app 目录
	appDir := execPath
	for !strings.HasSuffix(appDir, ".app") && appDir != "/" {
		appDir = filepath.Dir(appDir)
	}
	if !strings.HasSuffix(appDir, ".app") {
		// 非 .app bundle，按 Linux 方式处理
		return updateLinux(archivePath, execPath)
	}

	parentDir := filepath.Dir(appDir)

	// 解压 tar.gz 到临时目录
	tmpExtractDir, err := os.MkdirTemp("", "ops-cat-extract-*")
	if err != nil {
		return fmt.Errorf("create temp dir failed: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpExtractDir) }()

	if err := extractTarGz(archivePath, tmpExtractDir); err != nil {
		return fmt.Errorf("extract failed: %w", err)
	}

	// 找到解压出的 .app 目录
	newAppDir := filepath.Join(tmpExtractDir, "ops-cat.app")
	if _, err := os.Stat(newAppDir); err != nil {
		return fmt.Errorf("extracted app not found: %w", err)
	}

	// 备份旧的 .app
	backupDir := appDir + ".backup"
	_ = os.RemoveAll(backupDir)
	if err := os.Rename(appDir, backupDir); err != nil {
		return fmt.Errorf("backup old app failed: %w", err)
	}

	// 移动新的 .app 到原位置
	if err := os.Rename(newAppDir, filepath.Join(parentDir, "ops-cat.app")); err != nil {
		// 恢复备份
		_ = os.Rename(backupDir, appDir)
		return fmt.Errorf("install new app failed: %w", err)
	}

	_ = os.RemoveAll(backupDir)
	return nil
}

// updateLinux 更新 Linux 二进制
func updateLinux(archivePath, execPath string) error {
	// 解压 tar.gz
	tmpExtractDir, err := os.MkdirTemp("", "ops-cat-extract-*")
	if err != nil {
		return fmt.Errorf("create temp dir failed: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpExtractDir) }()

	if err := extractTarGz(archivePath, tmpExtractDir); err != nil {
		return fmt.Errorf("extract failed: %w", err)
	}

	newBin := filepath.Join(tmpExtractDir, "ops-cat")
	if _, err := os.Stat(newBin); err != nil {
		return fmt.Errorf("extracted binary not found: %w", err)
	}

	// 备份旧文件，替换新文件
	backupPath := execPath + ".backup"
	_ = os.Remove(backupPath)
	if err := os.Rename(execPath, backupPath); err != nil {
		return fmt.Errorf("backup old binary failed: %w", err)
	}

	if err := copyFile(newBin, execPath, 0755); err != nil {
		_ = os.Rename(backupPath, execPath)
		return fmt.Errorf("install new binary failed: %w", err)
	}

	_ = os.Remove(backupPath)
	return nil
}

// updateWindows 更新 Windows 二进制
func updateWindows(archivePath, execPath string) error {
	// 解压 zip
	tmpExtractDir, err := os.MkdirTemp("", "ops-cat-extract-*")
	if err != nil {
		return fmt.Errorf("create temp dir failed: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpExtractDir) }()

	if err := extractZip(archivePath, tmpExtractDir); err != nil {
		return fmt.Errorf("extract failed: %w", err)
	}

	newBin := filepath.Join(tmpExtractDir, "ops-cat.exe")
	if _, err := os.Stat(newBin); err != nil {
		return fmt.Errorf("extracted binary not found: %w", err)
	}

	// Windows 不能替换正在运行的 exe，重命名旧文件后复制新文件
	backupPath := execPath + ".old"
	_ = os.Remove(backupPath)
	if err := os.Rename(execPath, backupPath); err != nil {
		return fmt.Errorf("backup old binary failed: %w", err)
	}

	if err := copyFile(newBin, execPath, 0755); err != nil {
		_ = os.Rename(backupPath, execPath)
		return fmt.Errorf("install new binary failed: %w", err)
	}

	// 旧的 .old 文件留着，下次启动时可以清理
	return nil
}

// extractTarGz 解压 tar.gz 到指定目录
func extractTarGz(archivePath, destDir string) error {
	f, err := os.Open(archivePath) //nolint:gosec // extracting trusted archive
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		// 安全检查: 防止路径遍历
		target := filepath.Join(destDir, header.Name) //nolint:gosec // extracting trusted archive
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(destDir)+string(os.PathSeparator)) {
			continue
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)); err != nil { //nolint:gosec // extracting trusted archive
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil { //nolint:gosec // extracting trusted archive
				return err
			}
			outFile, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode)) //nolint:gosec // extracting trusted archive
			if err != nil {
				return err
			}
			if _, err := io.Copy(outFile, tr); err != nil { //nolint:gosec // trusted archive source
				_ = outFile.Close()
				return err
			}
			_ = outFile.Close()
		}
	}
	return nil
}

// extractZip 解压 zip 到指定目录
func extractZip(archivePath, destDir string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()

	for _, f := range r.File {
		target := filepath.Join(destDir, f.Name) //nolint:gosec // extracting trusted archive
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(destDir)+string(os.PathSeparator)) {
			continue
		}

		if f.FileInfo().IsDir() {
			_ = os.MkdirAll(target, 0755) //nolint:gosec // extracting trusted archive
			continue
		}

		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil { //nolint:gosec // extracting trusted archive
			return err
		}

		rc, err := f.Open()
		if err != nil {
			return err
		}
		outFile, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode()) //nolint:gosec // extracting trusted archive
		if err != nil {
			_ = rc.Close()
			return err
		}
		_, err = io.Copy(outFile, rc) //nolint:gosec // trusted archive source
		_ = outFile.Close()
		_ = rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// copyFile 复制文件
func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src) //nolint:gosec // copying trusted file
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm) //nolint:gosec // copying trusted file
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	_, err = io.Copy(out, in)
	return err
}

// progressReader 带进度回调的 reader
type progressReader struct {
	r          io.Reader
	total      int64
	downloaded int64
	onProgress func(downloaded, total int64)
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.r.Read(p)
	pr.downloaded += int64(n)
	pr.onProgress(pr.downloaded, pr.total)
	return n, err
}

// compareVersions 比较两个版本号 (如 "0.1.0" vs "0.2.0")
// 返回: >0 表示 a 更新, <0 表示 b 更新, 0 表示相同
func compareVersions(a, b string) int {
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")

	maxLen := len(aParts)
	if len(bParts) > maxLen {
		maxLen = len(bParts)
	}

	for i := 0; i < maxLen; i++ {
		var aNum, bNum int
		if i < len(aParts) {
			_, _ = fmt.Sscanf(aParts[i], "%d", &aNum)
		}
		if i < len(bParts) {
			_, _ = fmt.Sscanf(bParts[i], "%d", &bNum)
		}
		if aNum != bNum {
			return aNum - bNum
		}
	}
	return 0
}
