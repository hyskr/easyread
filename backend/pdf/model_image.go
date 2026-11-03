package pdf

import (
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"

	"github.com/hyskr/easyread/backend/store"
)

// EngineImage returns a source-stamped JPEG for model tools that do not accept
// WebP. Native rendering finishes and its child is reaped before Go saves JPEG.
func EngineImage(ctx context.Context, ws *store.Workspace, page int) (string, error) {
	if page < 1 {
		return "", fmt.Errorf("invalid PDF page: %d", page)
	}
	if e := secureDirs(ws.Root, "extract"); e != nil {
		return "", e
	}
	if e := acquireImage(ctx); e != nil {
		return "", e
	}
	defer releaseImage()
	source, e := ws.SourcePDF()
	if e != nil {
		return "", e
	}
	info, e := os.Stat(source)
	if e != nil {
		return "", e
	}
	stamp := fmt.Sprintf("go-model-jpeg-v1:%s:%d:%d", source, info.Size(), info.ModTime().UnixNano())
	out := filepath.Join(ws.Root, "extract", fmt.Sprintf("page-%03d.jpg", page))
	old, _ := os.ReadFile(out + ".stamp")
	if string(old) == stamp && fileExists(out) {
		return out, nil
	}
	tmp, e := os.MkdirTemp(filepath.Join(ws.Root, "extract"), ".model-image-")
	if e != nil {
		return "", e
	}
	defer os.RemoveAll(tmp)
	rendered := filepath.Join(tmp, "page.png")
	if e = render(ctx, source, page, rendered, 2); e != nil {
		return "", e
	}
	im, e := decodeImage(rendered)
	if e != nil {
		return "", e
	}
	if e = saveJPEG(out, im); e != nil {
		return "", e
	}
	if e = atomicFile(out+".stamp", []byte(stamp)); e != nil {
		return "", e
	}
	return out, nil
}
func saveJPEG(path string, im image.Image) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".model-jpeg-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = jpeg.Encode(f, im, &jpeg.Options{Quality: 82}); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}
