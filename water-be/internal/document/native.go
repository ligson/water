package document

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"

	pdfreader "github.com/ledongthuc/pdf"
)

const maxDocumentXMLPartBytes = 32 * 1024 * 1024

func extractNative(filePath string) (result Result, err error) {
	format := strings.TrimPrefix(strings.ToLower(path.Ext(filePath)), ".")
	switch format {
	case "docx":
		result.Content, err = extractDOCX(filePath)
	case "xlsx":
		result.Content, err = extractXLSX(filePath)
	case "pptx":
		result.Content, err = extractPPTX(filePath)
	case "pdf":
		result.Content, err = extractPDF(filePath)
	default:
		return Result{}, &Error{
			Code:    "native_document_type_unavailable",
			Message: fmt.Sprintf("内置解析器不支持 %s 格式", strings.ToUpper(format)),
			Hint:    "DOCX、XLSX、PPTX 和带文本层 PDF 可直接解析；旧 XLS 可选安装 MarkItDown 运行时。",
		}
	}
	if err != nil {
		return Result{}, err
	}
	result.Content = strings.TrimSpace(result.Content)
	if result.Content == "" {
		hint := "确认文档不是空文件或加密文件。"
		if format == "pdf" {
			hint = "PDF 可能是扫描件且没有文本层；需要启用本地 OCR 扩展。"
		}
		return Result{}, &Error{Code: "document_has_no_text", Message: "文档中没有提取到文本", Hint: hint}
	}
	result.Engine = "water-native"
	result.Format = format
	return result, nil
}

func extractDOCX(filePath string) (string, error) {
	archive, err := zip.OpenReader(filePath)
	if err != nil {
		return "", fmt.Errorf("open DOCX: %w", err)
	}
	defer archive.Close()

	parts := make([]*zip.File, 0)
	for _, file := range archive.File {
		name := file.Name
		if name == "word/document.xml" ||
			strings.HasPrefix(name, "word/header") && strings.HasSuffix(name, ".xml") ||
			strings.HasPrefix(name, "word/footer") && strings.HasSuffix(name, ".xml") ||
			name == "word/footnotes.xml" || name == "word/endnotes.xml" || name == "word/comments.xml" {
			parts = append(parts, file)
		}
	}
	sort.SliceStable(parts, func(i, j int) bool {
		return wordPartRank(parts[i].Name) < wordPartRank(parts[j].Name)
	})
	if len(parts) == 0 {
		return "", fmt.Errorf("DOCX is missing word/document.xml")
	}

	var output strings.Builder
	for _, part := range parts {
		raw, err := readZipPart(part)
		if err != nil {
			return "", err
		}
		content, err := extractParagraphXML(raw)
		if err != nil {
			return "", fmt.Errorf("parse %s: %w", part.Name, err)
		}
		if strings.TrimSpace(content) == "" {
			continue
		}
		if output.Len() > 0 {
			output.WriteString("\n\n")
		}
		if part.Name != "word/document.xml" {
			output.WriteString("## ")
			output.WriteString(path.Base(strings.TrimSuffix(part.Name, ".xml")))
			output.WriteString("\n\n")
		}
		output.WriteString(content)
	}
	return output.String(), nil
}

func wordPartRank(name string) string {
	if name == "word/document.xml" {
		return "0"
	}
	return "1" + name
}

func extractPPTX(filePath string) (string, error) {
	archive, err := zip.OpenReader(filePath)
	if err != nil {
		return "", fmt.Errorf("open PPTX: %w", err)
	}
	defer archive.Close()

	slides := numberedZipParts(archive.File, "ppt/slides/slide")
	notes := numberedZipParts(archive.File, "ppt/notesSlides/notesSlide")
	if len(slides) == 0 {
		return "", fmt.Errorf("PPTX contains no slides")
	}
	var output strings.Builder
	for index, slide := range slides {
		raw, err := readZipPart(slide)
		if err != nil {
			return "", err
		}
		content, err := extractParagraphXML(raw)
		if err != nil {
			return "", fmt.Errorf("parse %s: %w", slide.Name, err)
		}
		if output.Len() > 0 {
			output.WriteString("\n\n")
		}
		output.WriteString(fmt.Sprintf("## Slide %d\n\n%s", index+1, content))
		if index < len(notes) {
			noteRaw, readErr := readZipPart(notes[index])
			if readErr != nil {
				return "", readErr
			}
			noteContent, parseErr := extractParagraphXML(noteRaw)
			if parseErr != nil {
				return "", fmt.Errorf("parse %s: %w", notes[index].Name, parseErr)
			}
			if strings.TrimSpace(noteContent) != "" {
				output.WriteString("\n\n### Notes\n\n")
				output.WriteString(noteContent)
			}
		}
	}
	return output.String(), nil
}

func numberedZipParts(files []*zip.File, prefix string) []*zip.File {
	parts := make([]*zip.File, 0)
	for _, file := range files {
		if strings.HasPrefix(file.Name, prefix) && strings.HasSuffix(file.Name, ".xml") {
			parts = append(parts, file)
		}
	}
	sort.Slice(parts, func(i, j int) bool {
		return trailingNumber(parts[i].Name) < trailingNumber(parts[j].Name)
	})
	return parts
}

func trailingNumber(name string) int {
	name = strings.TrimSuffix(name, path.Ext(name))
	index := len(name)
	for index > 0 && name[index-1] >= '0' && name[index-1] <= '9' {
		index--
	}
	value, _ := strconv.Atoi(name[index:])
	return value
}

func extractParagraphXML(raw []byte) (string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	var output strings.Builder
	var paragraph strings.Builder
	inText := false
	style := ""
	flush := func() {
		value := strings.TrimSpace(paragraph.String())
		paragraph.Reset()
		if value == "" {
			style = ""
			return
		}
		if output.Len() > 0 {
			output.WriteByte('\n')
		}
		if level := headingLevel(style); level > 0 {
			output.WriteString(strings.Repeat("#", level))
			output.WriteByte(' ')
		}
		output.WriteString(value)
		style = ""
	}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		switch value := token.(type) {
		case xml.StartElement:
			switch value.Name.Local {
			case "t":
				inText = true
			case "tab":
				paragraph.WriteByte('\t')
			case "br":
				paragraph.WriteByte('\n')
			case "pStyle":
				style = xmlAttribute(value.Attr, "val")
			}
		case xml.CharData:
			if inText {
				paragraph.Write([]byte(value))
			}
		case xml.EndElement:
			switch value.Name.Local {
			case "t":
				inText = false
			case "p":
				flush()
			}
		}
	}
	flush()
	return output.String(), nil
}

func headingLevel(style string) int {
	lower := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(style), " ", ""))
	for level := 1; level <= 6; level++ {
		if lower == fmt.Sprintf("heading%d", level) || lower == fmt.Sprintf("标题%d", level) {
			return level
		}
	}
	return 0
}

func extractXLSX(filePath string) (string, error) {
	archive, err := zip.OpenReader(filePath)
	if err != nil {
		return "", fmt.Errorf("open XLSX: %w", err)
	}
	defer archive.Close()
	files := zipFileMap(archive.File)
	shared, err := readSharedStrings(files["xl/sharedStrings.xml"])
	if err != nil {
		return "", err
	}
	sheets, err := workbookSheets(files)
	if err != nil {
		return "", err
	}
	if len(sheets) == 0 {
		for name := range files {
			if strings.HasPrefix(name, "xl/worksheets/sheet") && strings.HasSuffix(name, ".xml") {
				sheets = append(sheets, sheetPart{Name: path.Base(strings.TrimSuffix(name, ".xml")), Path: name})
			}
		}
		sort.Slice(sheets, func(i, j int) bool { return trailingNumber(sheets[i].Path) < trailingNumber(sheets[j].Path) })
	}
	var output strings.Builder
	for _, sheet := range sheets {
		part := files[sheet.Path]
		if part == nil {
			continue
		}
		raw, readErr := readZipPart(part)
		if readErr != nil {
			return "", readErr
		}
		content, parseErr := extractSheetXML(raw, shared)
		if parseErr != nil {
			return "", fmt.Errorf("parse %s: %w", sheet.Path, parseErr)
		}
		if output.Len() > 0 {
			output.WriteString("\n\n")
		}
		output.WriteString("## Sheet: ")
		output.WriteString(sheet.Name)
		output.WriteString("\n\n")
		output.WriteString(content)
	}
	return output.String(), nil
}

type sheetPart struct {
	Name string
	Path string
}

func workbookSheets(files map[string]*zip.File) ([]sheetPart, error) {
	rels := make(map[string]string)
	if part := files["xl/_rels/workbook.xml.rels"]; part != nil {
		raw, err := readZipPart(part)
		if err != nil {
			return nil, err
		}
		decoder := xml.NewDecoder(bytes.NewReader(raw))
		for {
			token, err := decoder.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			if start, ok := token.(xml.StartElement); ok && start.Name.Local == "Relationship" {
				id := xmlAttribute(start.Attr, "Id")
				target := strings.TrimPrefix(xmlAttribute(start.Attr, "Target"), "/")
				if !strings.HasPrefix(target, "xl/") {
					target = path.Clean(path.Join("xl", target))
				}
				rels[id] = target
			}
		}
	}
	part := files["xl/workbook.xml"]
	if part == nil {
		return nil, nil
	}
	raw, err := readZipPart(part)
	if err != nil {
		return nil, err
	}
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	items := make([]sheetPart, 0)
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if start, ok := token.(xml.StartElement); ok && start.Name.Local == "sheet" {
			name := xmlAttribute(start.Attr, "name")
			relID := xmlAttribute(start.Attr, "id")
			if target := rels[relID]; target != "" {
				items = append(items, sheetPart{Name: name, Path: target})
			}
		}
	}
	return items, nil
}

func readSharedStrings(part *zip.File) ([]string, error) {
	if part == nil {
		return nil, nil
	}
	raw, err := readZipPart(part)
	if err != nil {
		return nil, err
	}
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	items := make([]string, 0)
	var current strings.Builder
	inItem := false
	inText := false
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch value := token.(type) {
		case xml.StartElement:
			if value.Name.Local == "si" {
				inItem = true
				current.Reset()
			} else if value.Name.Local == "t" && inItem {
				inText = true
			}
		case xml.CharData:
			if inText {
				current.Write([]byte(value))
			}
		case xml.EndElement:
			if value.Name.Local == "t" {
				inText = false
			} else if value.Name.Local == "si" {
				items = append(items, current.String())
				inItem = false
			}
		}
	}
	return items, nil
}

func extractSheetXML(raw []byte, shared []string) (string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	var output strings.Builder
	rowCells := make([]string, 0)
	cellRef := ""
	cellType := ""
	cellValue := ""
	cellFormula := ""
	inValue := false
	inFormula := false
	inInlineText := false
	flushCell := func() {
		value := cellValue
		if cellType == "s" {
			if index, err := strconv.Atoi(strings.TrimSpace(cellValue)); err == nil && index >= 0 && index < len(shared) {
				value = shared[index]
			}
		} else if cellType == "b" {
			if strings.TrimSpace(cellValue) == "1" {
				value = "true"
			} else {
				value = "false"
			}
		}
		if cellFormula != "" {
			value = "=" + cellFormula + stringWhen(value != "", " (cached: "+value+")")
		}
		value = strings.TrimSpace(value)
		if value != "" || cellFormula != "" {
			rowCells = append(rowCells, fmt.Sprintf("%s: %s", stringWithDefault(cellRef, "?"), value))
		}
		cellRef, cellType, cellValue, cellFormula = "", "", "", ""
	}
	flushRow := func() {
		if len(rowCells) == 0 {
			return
		}
		if output.Len() > 0 {
			output.WriteByte('\n')
		}
		output.WriteString("- ")
		output.WriteString(strings.Join(rowCells, " | "))
		rowCells = rowCells[:0]
	}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		switch value := token.(type) {
		case xml.StartElement:
			switch value.Name.Local {
			case "c":
				cellRef = xmlAttribute(value.Attr, "r")
				cellType = xmlAttribute(value.Attr, "t")
			case "v":
				inValue = true
			case "f":
				inFormula = true
			case "t":
				if cellType == "inlineStr" {
					inInlineText = true
				}
			}
		case xml.CharData:
			switch {
			case inValue:
				cellValue += string(value)
			case inFormula:
				cellFormula += string(value)
			case inInlineText:
				cellValue += string(value)
			}
		case xml.EndElement:
			switch value.Name.Local {
			case "v":
				inValue = false
			case "f":
				inFormula = false
			case "t":
				inInlineText = false
			case "c":
				flushCell()
			case "row":
				flushRow()
			}
		}
	}
	flushCell()
	flushRow()
	return output.String(), nil
}

func extractPDF(filePath string) (content string, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("parse PDF: %v", recovered)
		}
	}()
	file, reader, err := pdfreader.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("open PDF: %w", err)
	}
	defer file.Close()
	plainText, err := reader.GetPlainText()
	if err != nil {
		return "", fmt.Errorf("extract PDF text: %w", err)
	}
	var output bytes.Buffer
	if _, err := output.ReadFrom(io.LimitReader(plainText, maxDocumentXMLPartBytes)); err != nil {
		return "", fmt.Errorf("read PDF text: %w", err)
	}
	return output.String(), nil
}

func readZipPart(file *zip.File) ([]byte, error) {
	if file.UncompressedSize64 > maxDocumentXMLPartBytes {
		return nil, fmt.Errorf("document XML part %s exceeds 32 MiB", file.Name)
	}
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	raw, err := io.ReadAll(io.LimitReader(reader, maxDocumentXMLPartBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxDocumentXMLPartBytes {
		return nil, fmt.Errorf("document XML part %s exceeds 32 MiB", file.Name)
	}
	return raw, nil
}

func zipFileMap(files []*zip.File) map[string]*zip.File {
	result := make(map[string]*zip.File, len(files))
	for _, file := range files {
		result[path.Clean(strings.TrimPrefix(file.Name, "/"))] = file
	}
	return result
}

func xmlAttribute(attributes []xml.Attr, name string) string {
	for _, attribute := range attributes {
		if strings.EqualFold(attribute.Name.Local, name) {
			return attribute.Value
		}
	}
	return ""
}

func stringWhen(condition bool, value string) string {
	if condition {
		return value
	}
	return ""
}
