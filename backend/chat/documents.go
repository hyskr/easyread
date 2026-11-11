package chat

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"github.com/hyskr/easyread/backend/pdf"
	"path/filepath"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

func extractDocuments(ctx context.Context, value any) ([]any, error) {
	result := []any{}
	if value == nil {
		return result, nil
	}
	files, ok := value.([]any)
	if !ok || len(files) > 2 {
		return nil, fmt.Errorf("每次最多添加 2 个文件")
	}
	for _, raw := range files {
		file, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("附件格式无效")
		}
		name := filepath.Base(strings.ReplaceAll(str(file["name"]), "\\", "/"))
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".pdf" && ext != ".txt" && ext != ".md" {
			return nil, fmt.Errorf("请选择 PDF、TXT 或 Markdown 文件")
		}
		encoded := str(file["data"])
		if len(encoded) > 4*((8*1024*1024+2)/3) {
			return nil, fmt.Errorf("每个文件最多 8 MB")
		}
		data, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil || len(data) > 8*1024*1024 {
			return nil, fmt.Errorf("附件格式无效")
		}
		var text string
		if ext == ".pdf" {
			text, err = pdf.DocumentText(ctx, data)
			if err != nil {
				return nil, err
			}
		} else if len(data) >= 2 && (data[0] == 0xff && data[1] == 0xfe || data[0] == 0xfe && data[1] == 0xff) {
			if len(data)%2 != 0 {
				return nil, fmt.Errorf("文本编码无效")
			}
			var order binary.ByteOrder = binary.LittleEndian
			if data[0] == 0xfe {
				order = binary.BigEndian
			}
			units := []uint16{}
			for i := 2; i < len(data); i += 2 {
				units = append(units, order.Uint16(data[i:i+2]))
			}
			text = string(utf16.Decode(units))
		} else {
			if !utf8.Valid(data) {
				return nil, fmt.Errorf("请使用 UTF-8 或带 BOM 的 UTF-16 文本文件")
			}
			text = strings.TrimPrefix(string(data), "\ufeff")
		}
		if strings.ContainsRune(text, 0) {
			return nil, fmt.Errorf("文本编码无效")
		}
		if strings.TrimSpace(text) == "" {
			return nil, fmt.Errorf("附件没有可读取的文字，扫描版 PDF 请先做文字识别")
		}
		truncated := len([]rune(text)) > 30000
		result = append(result, M{"name": name, "text": clip(text, 30000), "truncated": truncated})
	}
	return result, nil
}
