package pdf

import (
	"context"
	"os"
	"path/filepath"
)

func DocumentText(ctx context.Context, data []byte) (string, error) {
	dir, err := os.MkdirTemp("", "easyread-document-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "document.pdf")
	if err = os.WriteFile(path, data, 0600); err != nil {
		return "", err
	}
	text, err := run(ctx, "pdftotext", "-f", "1", "-l", "150", "-enc", "UTF-8", path, "-")
	return string(text), err
}
