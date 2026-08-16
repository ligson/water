package document

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const defaultExtractTimeout = 90 * time.Second

const extractScript = `
import sys
from markitdown import MarkItDown

result = MarkItDown(enable_plugins=False).convert_local(sys.argv[1])
content = getattr(result, "text_content", None)
if content is None:
    content = getattr(result, "markdown", "")
sys.stdout.write(content or "")
`

var supportedExtensions = map[string]bool{
	".docx": true,
	".pdf":  true,
	".pptx": true,
	".xls":  true,
	".xlsx": true,
}

type Error struct {
	Code    string
	Message string
	Hint    string
}

func (e *Error) Error() string { return e.Message }

type Result struct {
	Content string
	Engine  string
	Format  string
}

type cacheEntry struct {
	Size    int64
	ModTime int64
	Result  Result
}

// Extractor converts supported local documents to Markdown. The portable Go
// engine is the default; MarkItDown is an optional enhancement.
type Extractor struct {
	pythonPath string
	timeout    time.Duration

	mu    sync.Mutex
	cache map[string]cacheEntry
}

func NewExtractor(pythonPath string) *Extractor {
	return &Extractor{
		pythonPath: strings.TrimSpace(pythonPath),
		timeout:    defaultExtractTimeout,
		cache:      make(map[string]cacheEntry),
	}
}

func SupportsPath(path string) bool {
	return supportedExtensions[strings.ToLower(filepath.Ext(path))]
}

func (e *Extractor) Extract(ctx context.Context, path string) (Result, error) {
	format := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	if !SupportsPath(path) {
		return Result{}, &Error{
			Code:    "unsupported_document_type",
			Message: fmt.Sprintf("不支持读取 %s 文档", strings.ToUpper(format)),
			Hint:    "当前支持 PDF、DOCX、XLS/XLSX 和 PPTX；旧 DOC/PPT 请先用 LibreOffice 转换。",
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return Result{}, fmt.Errorf("inspect document: %w", err)
	}
	engine := selectedEngine()
	cacheKey := engine + "|" + path
	if cached, ok := e.cached(cacheKey, info); ok {
		return cached, nil
	}
	if engine != "markitdown" {
		result, nativeErr := extractNative(path)
		if nativeErr == nil {
			e.storeCache(cacheKey, info, result)
			return result, nil
		}
		return Result{}, nativeErr
	}

	result, err := e.extractMarkItDown(ctx, path, format)
	if err != nil {
		return Result{}, err
	}
	e.storeCache(cacheKey, info, result)
	return result, nil
}

func (e *Extractor) extractMarkItDown(ctx context.Context, path string, format string) (Result, error) {
	pythonPath := e.resolvePython()
	if pythonPath == "" {
		return Result{}, runtimeUnavailableError("")
	}
	runCtx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, pythonPath, "-I", "-c", extractScript, path)
	cmd.Env = append(os.Environ(), "PYTHONNOUSERSITE=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Result{}, runtimeUnavailableError(pythonPath)
		}
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return Result{}, &Error{
				Code:    "document_extract_timeout",
				Message: "文档解析超过 90 秒，已停止",
				Hint:    "尝试拆分文档、移除异常嵌入对象，或使用更轻量的版本。",
			}
		}
		message := strings.TrimSpace(string(output))
		if strings.Contains(message, "No module named 'markitdown'") || strings.Contains(message, "No module named \"markitdown\"") {
			return Result{}, runtimeUnavailableError(pythonPath)
		}
		if len(message) > 2000 {
			message = message[len(message)-2000:]
		}
		return Result{}, &Error{
			Code:    "document_extract_failed",
			Message: "文档解析失败: " + stringWithDefault(message, err.Error()),
			Hint:    "确认文件未损坏、未加密，并检查文档运行时依赖是否完整。",
		}
	}
	result := Result{
		Content: strings.TrimSpace(string(output)),
		Engine:  "markitdown",
		Format:  format,
	}
	return result, nil
}

func selectedEngine() string {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("WATER_DOCUMENT_ENGINE")), "markitdown") {
		return "markitdown"
	}
	return "native"
}

func (e *Extractor) cached(key string, info os.FileInfo) (Result, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	item, ok := e.cache[key]
	if !ok || item.Size != info.Size() || item.ModTime != info.ModTime().UnixNano() {
		return Result{}, false
	}
	return item.Result, true
}

func (e *Extractor) storeCache(key string, info os.FileInfo, result Result) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.cache) >= 16 {
		clear(e.cache)
	}
	e.cache[key] = cacheEntry{Size: info.Size(), ModTime: info.ModTime().UnixNano(), Result: result}
}

func (e *Extractor) resolvePython() string {
	if e.pythonPath != "" {
		return e.pythonPath
	}
	if configured := strings.TrimSpace(os.Getenv("WATER_DOCUMENT_PYTHON")); configured != "" {
		return configured
	}
	candidates := []string{
		filepath.Join(".venv-document", pythonExecutable()),
		filepath.Join("water-be", ".venv-document", pythonExecutable()),
	}
	for _, candidate := range candidates {
		if absolute, err := filepath.Abs(candidate); err == nil {
			if info, statErr := os.Stat(absolute); statErr == nil && !info.IsDir() {
				return absolute
			}
		}
	}
	path, _ := exec.LookPath("python3")
	return path
}

func pythonExecutable() string {
	if runtime.GOOS == "windows" {
		return filepath.Join("Scripts", "python.exe")
	}
	return filepath.Join("bin", "python")
}

func runtimeUnavailableError(path string) error {
	detail := ""
	if path != "" {
		detail = "（当前 Python: " + path + "）"
	}
	return &Error{
		Code:    "document_runtime_unavailable",
		Message: "可选 MarkItDown 文档运行时尚未安装" + detail,
		Hint:    "DOCX、XLSX、PPTX 和带文本层 PDF 可直接使用内置引擎；旧 XLS 或增强解析可执行 ./scripts/setup-document-runtime.sh。",
	}
}

func stringWithDefault(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
