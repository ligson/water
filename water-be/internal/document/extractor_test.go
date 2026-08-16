package document

import (
	"archive/zip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSupportsPath(t *testing.T) {
	for _, path := range []string{"report.PDF", "plan.docx", "data.xlsx", "legacy.xls", "slides.pptx"} {
		if !SupportsPath(path) {
			t.Fatalf("expected %s to be supported", path)
		}
	}
	for _, path := range []string{"legacy.doc", "legacy.ppt", "archive.zip", "note.txt"} {
		if SupportsPath(path) {
			t.Fatalf("expected %s to be unsupported", path)
		}
	}
}

func TestMissingRuntimeReturnsStructuredError(t *testing.T) {
	t.Setenv("WATER_DOCUMENT_ENGINE", "markitdown")
	path := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(path, []byte("pdf"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	_, err := NewExtractor(filepath.Join(t.TempDir(), "missing-python")).Extract(context.Background(), path)
	var documentErr *Error
	if !errors.As(err, &documentErr) {
		t.Fatalf("expected structured document error, got %v", err)
	}
	if documentErr.Code != "document_runtime_unavailable" || documentErr.Hint == "" {
		t.Fatalf("unexpected runtime error: %#v", documentErr)
	}
}

func TestInstalledRuntimeExtractsDOCX(t *testing.T) {
	t.Setenv("WATER_DOCUMENT_ENGINE", "markitdown")
	pythonPath := strings.TrimSpace(os.Getenv("WATER_DOCUMENT_TEST_PYTHON"))
	if pythonPath == "" {
		t.Skip("set WATER_DOCUMENT_TEST_PYTHON to run the MarkItDown integration test")
	}
	path := filepath.Join(t.TempDir(), "report.docx")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create fixture: %v", err)
	}
	archive := zip.NewWriter(file)
	entries := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":         `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml":   `<?xml version="1.0" encoding="UTF-8"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>WATER DOCUMENT BRIDGE</w:t></w:r></w:p></w:body></w:document>`,
	}
	for name, content := range entries {
		writer, createErr := archive.Create(name)
		if createErr != nil {
			t.Fatalf("create %s: %v", name, createErr)
		}
		if _, writeErr := writer.Write([]byte(content)); writeErr != nil {
			t.Fatalf("write %s: %v", name, writeErr)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("close archive: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}

	result, err := NewExtractor(pythonPath).Extract(context.Background(), path)
	if err != nil {
		t.Fatalf("extract document: %v", err)
	}
	if result.Engine != "markitdown" || !strings.Contains(result.Content, "WATER DOCUMENT BRIDGE") {
		t.Fatalf("unexpected extracted result: %#v", result)
	}
}
