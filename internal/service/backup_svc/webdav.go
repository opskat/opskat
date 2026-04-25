package backup_svc

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

const (
	webDAVBackupFilename   = gistBackupFilename
	webDAVDefaultDirectory = "opskat"
)

var webDAVHTTPClient = &http.Client{Timeout: 30 * time.Second}

// WebDAVConfig contains the connection details used for WebDAV backup transport.
type WebDAVConfig struct {
	URL      string `json:"url"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

// WebDAVBackupInfo is the frontend-facing metadata for a remote backup file.
type WebDAVBackupInfo struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	UpdatedAt string `json:"updatedAt"`
	Size      int64  `json:"size"`
}

// CreateOrUpdateWebDAVBackup uploads the canonical encrypted backup file to WebDAV.
func CreateOrUpdateWebDAVBackup(cfg WebDAVConfig, content []byte) (*WebDAVBackupInfo, error) {
	dirURL, err := webDAVDirectoryURL(cfg.URL)
	if err != nil {
		return nil, err
	}
	if err := ensureWebDAVDirectory(cfg, dirURL); err != nil {
		return nil, err
	}

	fileURL, err := webDAVFileURL(cfg.URL, webDAVBackupFilename)
	if err != nil {
		return nil, err
	}
	resp, body, err := webDAVRequest(cfg, http.MethodPut, fileURL, content, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		return nil, fmt.Errorf("WebDAV upload failed: HTTP %d: %s", resp.StatusCode, string(body))
	}
	return &WebDAVBackupInfo{
		Name:      webDAVBackupFilename,
		Path:      fileURL,
		UpdatedAt: time.Now().Format(time.RFC3339),
		Size:      int64(len(content)),
	}, nil
}

// ListWebDAVBackups lists OpsKat backup files from the configured WebDAV directory.
func ListWebDAVBackups(cfg WebDAVConfig) ([]*WebDAVBackupInfo, error) {
	dirURL, err := webDAVDirectoryURL(cfg.URL)
	if err != nil {
		return nil, err
	}
	body := []byte(`<?xml version="1.0" encoding="utf-8"?><propfind xmlns="DAV:"><prop><getlastmodified/><getcontentlength/><resourcetype/></prop></propfind>`)
	resp, respBody, err := webDAVRequest(cfg, "PROPFIND", dirURL, body, map[string]string{
		"Depth":        "1",
		"Content-Type": "application/xml; charset=utf-8",
	})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 207 && resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			return []*WebDAVBackupInfo{}, nil
		}
		return nil, fmt.Errorf("WebDAV list failed: HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	return parseWebDAVBackupList(respBody)
}

// GetWebDAVBackupContent downloads a selected OpsKat backup file from WebDAV.
func GetWebDAVBackupContent(cfg WebDAVConfig, name string) ([]byte, error) {
	fileURL, err := webDAVFileURL(cfg.URL, name)
	if err != nil {
		return nil, err
	}
	resp, body, err := webDAVRequest(cfg, http.MethodGet, fileURL, nil, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("WebDAV download failed: HTTP %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

// TestWebDAVConnection verifies that the configured WebDAV directory is reachable.
func TestWebDAVConnection(cfg WebDAVConfig) error {
	_, err := ListWebDAVBackups(cfg)
	return err
}

func ensureWebDAVDirectory(cfg WebDAVConfig, dirURL string) error {
	resp, body, err := webDAVRequest(cfg, "MKCOL", dirURL, nil, nil)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusOK ||
		resp.StatusCode == http.StatusCreated ||
		resp.StatusCode == http.StatusNoContent ||
		resp.StatusCode == http.StatusMethodNotAllowed {
		return nil
	}
	return fmt.Errorf("WebDAV create directory failed: HTTP %d: %s", resp.StatusCode, string(body))
}

func webDAVRequest(cfg WebDAVConfig, method, target string, body []byte, headers map[string]string) (*http.Response, []byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, target, reader)
	if err != nil {
		return nil, nil, fmt.Errorf("create WebDAV request: %w", err)
	}
	if cfg.Username != "" || cfg.Password != "" {
		req.SetBasicAuth(cfg.Username, cfg.Password)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := webDAVHTTPClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("request WebDAV: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("read WebDAV response: %w", err)
	}
	return resp, respBody, nil
}

func webDAVDirectoryURL(raw string) (string, error) {
	u, err := parseWebDAVBaseURL(raw)
	if err != nil {
		return "", err
	}
	u.Path = webDAVStoragePath(u.Path)
	return u.String(), nil
}

func webDAVFileURL(raw, name string) (string, error) {
	if name == "" {
		name = webDAVBackupFilename
	}
	if name != path.Base(name) || strings.Contains(name, "\\") {
		return "", fmt.Errorf("invalid WebDAV backup name %q", name)
	}
	u, err := parseWebDAVBaseURL(raw)
	if err != nil {
		return "", err
	}
	u.Path = strings.TrimRight(webDAVStoragePath(u.Path), "/") + "/" + url.PathEscape(name)
	return u.String(), nil
}

func webDAVStoragePath(rawPath string) string {
	cleanPath := strings.TrimRight(rawPath, "/")
	if path.Base(cleanPath) == webDAVDefaultDirectory {
		return cleanPath + "/"
	}
	if cleanPath == "" {
		return "/" + webDAVDefaultDirectory + "/"
	}
	return cleanPath + "/" + webDAVDefaultDirectory + "/"
}

func parseWebDAVBaseURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("WebDAV URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse WebDAV URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("WebDAV URL must start with http:// or https://")
	}
	if u.Host == "" {
		return nil, fmt.Errorf("WebDAV URL must include a host")
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u, nil
}

type webDAVMultiStatus struct {
	Responses []webDAVResponse `xml:"response"`
}

type webDAVResponse struct {
	Href     string             `xml:"href"`
	Propstat []webDAVPropStatus `xml:"propstat"`
}

type webDAVPropStatus struct {
	Status string     `xml:"status"`
	Prop   webDAVProp `xml:"prop"`
}

type webDAVProp struct {
	GetContentLength string             `xml:"getcontentlength"`
	GetLastModified  string             `xml:"getlastmodified"`
	ResourceType     webDAVResourceType `xml:"resourcetype"`
}

type webDAVResourceType struct {
	Collection *struct{} `xml:"collection"`
}

func parseWebDAVBackupList(data []byte) ([]*WebDAVBackupInfo, error) {
	var ms webDAVMultiStatus
	if err := xml.Unmarshal(data, &ms); err != nil {
		return nil, fmt.Errorf("parse WebDAV list: %w", err)
	}
	result := make([]*WebDAVBackupInfo, 0)
	for _, response := range ms.Responses {
		if response.Href == "" {
			continue
		}
		prop := response.bestProp()
		if prop.ResourceType.Collection != nil {
			continue
		}
		name := webDAVNameFromHref(response.Href)
		if !isOpsKatBackupName(name) {
			continue
		}
		size, _ := strconv.ParseInt(strings.TrimSpace(prop.GetContentLength), 10, 64)
		result = append(result, &WebDAVBackupInfo{
			Name:      name,
			Path:      response.Href,
			UpdatedAt: strings.TrimSpace(prop.GetLastModified),
			Size:      size,
		})
	}
	return result, nil
}

func (r webDAVResponse) bestProp() webDAVProp {
	for _, ps := range r.Propstat {
		if ps.Status == "" || strings.Contains(ps.Status, " 200 ") {
			return ps.Prop
		}
	}
	if len(r.Propstat) > 0 {
		return r.Propstat[0].Prop
	}
	return webDAVProp{}
}

func webDAVNameFromHref(href string) string {
	if u, err := url.Parse(href); err == nil {
		href = u.Path
	}
	name, err := url.PathUnescape(path.Base(strings.TrimRight(href, "/")))
	if err != nil {
		return path.Base(strings.TrimRight(href, "/"))
	}
	return name
}

func isOpsKatBackupName(name string) bool {
	return name == webDAVBackupFilename || (strings.HasPrefix(name, "opskat-backup-") && strings.HasSuffix(name, ".json"))
}
