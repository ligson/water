package api

import (
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/ligson/water/water-be/internal/task"
	"github.com/ligson/water/water-be/internal/uid"
)

const (
	maxTurnAttachments      = 6
	maxTurnAttachmentBytes  = 8 * 1024 * 1024
	maxTurnAttachmentsBytes = 20 * 1024 * 1024
	maxTurnRequestBodyBytes = 28 * 1024 * 1024
)

type turnAttachmentRequest struct {
	Name     string `json:"name"`
	MIMEType string `json:"mimeType"`
	DataURL  string `json:"dataUrl"`
}

func storeTurnAttachments(rootPath string, taskID string, requests []turnAttachmentRequest) ([]task.Attachment, string, error) {
	if len(requests) == 0 {
		return make([]task.Attachment, 0), "", nil
	}
	if len(requests) > maxTurnAttachments {
		return nil, "", fmt.Errorf("一次最多上传 %d 个附件", maxTurnAttachments)
	}

	dir := filepath.Join(rootPath, ".water", "attachments", taskID, uid.New("upload"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", fmt.Errorf("create attachment directory: %w", err)
	}
	_ = excludeWorkspaceAttachmentsFromGit(rootPath)
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(dir)
		}
	}()

	items := make([]task.Attachment, 0, len(requests))
	totalBytes := 0
	for _, request := range requests {
		name := safeAttachmentName(request.Name)
		if name == "" {
			return nil, "", errors.New("附件名称不能为空")
		}
		mimeType, content, err := decodeAttachmentDataURL(request.DataURL, request.MIMEType)
		if err != nil {
			return nil, "", fmt.Errorf("附件 %s 无效: %w", name, err)
		}
		if len(content) == 0 {
			return nil, "", fmt.Errorf("附件 %s 内容为空", name)
		}
		if len(content) > maxTurnAttachmentBytes {
			return nil, "", fmt.Errorf("附件 %s 超过 8 MiB", name)
		}
		totalBytes += len(content)
		if totalBytes > maxTurnAttachmentsBytes {
			return nil, "", errors.New("附件总大小超过 20 MiB")
		}

		id := uid.New("att")
		path := filepath.Join(dir, id+"-"+name)
		if err := os.WriteFile(path, content, 0o644); err != nil {
			return nil, "", fmt.Errorf("store attachment %s: %w", name, err)
		}
		kind := "file"
		if strings.HasPrefix(mimeType, "image/") {
			kind = "image"
		}
		items = append(items, task.Attachment{
			ID:       id,
			Name:     name,
			MIMEType: mimeType,
			Kind:     kind,
			Path:     path,
			Size:     int64(len(content)),
		})
	}

	cleanup = false
	return items, dir, nil
}

func decodeAttachmentDataURL(dataURL string, requestedMIME string) (string, []byte, error) {
	metadata, encoded, ok := strings.Cut(strings.TrimSpace(dataURL), ",")
	if !ok || !strings.HasPrefix(metadata, "data:") || !strings.HasSuffix(strings.ToLower(metadata), ";base64") {
		return "", nil, errors.New("必须使用 base64 data URL")
	}
	declaredMIME, err := normalizeAttachmentMIME(strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(metadata, "data:"), ";base64")))
	if err != nil {
		return "", nil, fmt.Errorf("data URL MIME 无效: %w", err)
	}
	content, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", nil, errors.New("base64 内容无法解码")
	}
	detectedMIME := http.DetectContentType(content)
	mimeType, err := normalizeAttachmentMIME(requestedMIME)
	if err != nil {
		return "", nil, fmt.Errorf("附件 MIME 无效: %w", err)
	}
	if mimeType == "" {
		mimeType = declaredMIME
	}
	if mimeType == "" {
		mimeType = detectedMIME
	}
	if strings.HasPrefix(mimeType, "image/") || strings.HasPrefix(declaredMIME, "image/") {
		if !strings.HasPrefix(detectedMIME, "image/") {
			return "", nil, errors.New("图片 MIME 与实际内容不匹配")
		}
		mimeType = detectedMIME
	}
	return mimeType, content, nil
}

func normalizeAttachmentMIME(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return "", err
	}
	return strings.ToLower(mediaType), nil
}

func safeAttachmentName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	base := filepath.Base(strings.ReplaceAll(value, "\\", "/"))
	if base == "." || base == ".." {
		return ""
	}
	base = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '/' || r == '\\' || r == ':' {
			return -1
		}
		return r
	}, base)
	base = strings.TrimSpace(base)
	if len([]byte(base)) > 180 {
		ext := filepath.Ext(base)
		stem := strings.TrimSuffix(base, ext)
		runes := []rune(stem)
		for len([]byte(string(runes)+ext)) > 180 && len(runes) > 0 {
			runes = runes[:len(runes)-1]
		}
		base = string(runes) + ext
	}
	return base
}

func excludeWorkspaceAttachmentsFromGit(rootPath string) error {
	gitPath := filepath.Join(rootPath, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return nil
	}
	gitDir := gitPath
	if !info.IsDir() {
		raw, readErr := os.ReadFile(gitPath)
		if readErr != nil {
			return readErr
		}
		line := strings.TrimSpace(string(raw))
		if !strings.HasPrefix(line, "gitdir:") {
			return nil
		}
		gitDir = strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
		if !filepath.IsAbs(gitDir) {
			gitDir = filepath.Join(rootPath, gitDir)
		}
	}
	excludePath := filepath.Join(gitDir, "info", "exclude")
	raw, err := os.ReadFile(excludePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	const rule = ".water/attachments/"
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == rule {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(excludePath), 0o755); err != nil {
		return err
	}
	prefix := ""
	if len(raw) > 0 && raw[len(raw)-1] != '\n' {
		prefix = "\n"
	}
	file, err := os.OpenFile(excludePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.WriteString(prefix + rule + "\n")
	return err
}
