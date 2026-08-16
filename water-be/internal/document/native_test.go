package document

import (
	"archive/zip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeExtractorReadsPortableFormats(t *testing.T) {
	t.Setenv("WATER_DOCUMENT_ENGINE", "native")
	root := t.TempDir()
	fixtures := map[string]struct {
		path  string
		token string
	}{
		"docx": {path: filepath.Join(root, "sample.docx"), token: "WATER DOCX NATIVE"},
		"xlsx": {path: filepath.Join(root, "sample.xlsx"), token: "WATER XLSX NATIVE"},
		"pptx": {path: filepath.Join(root, "sample.pptx"), token: "WATER PPTX NATIVE"},
		"pdf":  {path: filepath.Join(root, "sample.pdf"), token: "WATER PDF NATIVE"},
	}
	writeZipFixture(t, fixtures["docx"].path, map[string]string{
		"word/document.xml": `<?xml version="1.0"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>WATER DOCX NATIVE</w:t></w:r></w:p></w:body></w:document>`,
	})
	writeZipFixture(t, fixtures["xlsx"].path, map[string]string{
		"xl/workbook.xml":            `<?xml version="1.0"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="WaterData" sheetId="1" r:id="rId1"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Target="worksheets/sheet1.xml"/></Relationships>`,
		"xl/sharedStrings.xml":       `<?xml version="1.0"?><sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><si><t>WATER XLSX NATIVE</t></si></sst>`,
		"xl/worksheets/sheet1.xml":   `<?xml version="1.0"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1"><f>40+2</f><v>42</v></c></row></sheetData></worksheet>`,
	})
	writeZipFixture(t, fixtures["pptx"].path, map[string]string{
		"ppt/slides/slide1.xml": `<?xml version="1.0"?><p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><p:cSld><p:spTree><p:sp><p:txBody><a:p><a:r><a:t>WATER PPTX NATIVE</a:t></a:r></a:p></p:txBody></p:sp></p:spTree></p:cSld></p:sld>`,
	})
	writeMinimalPDF(t, fixtures["pdf"].path, fixtures["pdf"].token)

	extractor := NewExtractor(filepath.Join(root, "python-must-not-be-used"))
	for format, fixture := range fixtures {
		t.Run(format, func(t *testing.T) {
			result, err := extractor.Extract(context.Background(), fixture.path)
			if err != nil {
				t.Fatalf("extract %s: %v", format, err)
			}
			if result.Engine != "water-native" || !strings.Contains(result.Content, fixture.token) {
				t.Fatalf("unexpected native %s result: %#v", format, result)
			}
		})
	}
}

func TestNativeExtractorDoesNotRequirePythonForLegacyXLS(t *testing.T) {
	t.Setenv("WATER_DOCUMENT_ENGINE", "native")
	filePath := filepath.Join(t.TempDir(), "legacy.xls")
	if err := os.WriteFile(filePath, []byte("legacy workbook"), 0o644); err != nil {
		t.Fatalf("write XLS fixture: %v", err)
	}

	result, err := NewExtractor(filepath.Join(t.TempDir(), "python-must-not-be-used")).Extract(context.Background(), filePath)
	if err == nil {
		t.Fatalf("expected unsupported native XLS error, got %#v", result)
	}
	documentErr, ok := err.(*Error)
	if !ok || documentErr.Code != "native_document_type_unavailable" {
		t.Fatalf("unexpected XLS error: %#v", err)
	}
}

func writeZipFixture(t *testing.T, filePath string, entries map[string]string) {
	t.Helper()
	file, err := os.Create(filePath)
	if err != nil {
		t.Fatalf("create %s: %v", filePath, err)
	}
	archive := zip.NewWriter(file)
	for name, content := range entries {
		writer, err := archive.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("close archive: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close %s: %v", filePath, err)
	}
}

func writeMinimalPDF(t *testing.T, filePath string, text string) {
	t.Helper()
	content := []byte("BT /F1 18 Tf 72 720 Td (" + text + ") Tj ET")
	objects := [][]byte{
		[]byte(`<< /Type /Catalog /Pages 2 0 R >>`),
		[]byte(`<< /Type /Pages /Kids [3 0 R] /Count 1 >>`),
		[]byte(`<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>`),
		[]byte(`<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>`),
		[]byte(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content)),
	}
	pdf := []byte("%PDF-1.4\n")
	offsets := []int{0}
	for index, object := range objects {
		offsets = append(offsets, len(pdf))
		pdf = append(pdf, []byte(fmt.Sprintf("%d 0 obj\n", index+1))...)
		pdf = append(pdf, object...)
		pdf = append(pdf, []byte("\nendobj\n")...)
	}
	xref := len(pdf)
	pdf = append(pdf, []byte(fmt.Sprintf("xref\n0 %d\n0000000000 65535 f \n", len(objects)+1))...)
	for _, offset := range offsets[1:] {
		pdf = append(pdf, []byte(fmt.Sprintf("%010d 00000 n \n", offset))...)
	}
	pdf = append(pdf, []byte(fmt.Sprintf("trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref))...)
	if err := os.WriteFile(filePath, pdf, 0o644); err != nil {
		t.Fatalf("write PDF: %v", err)
	}
}
