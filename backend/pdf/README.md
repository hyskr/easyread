# Native PDF processing

This package contains the Go implementation of PDF preparation, selection caches,
layout matching, figure crops, page variants, and annotation-link backfill. It
never starts Python. Poppler utilities run in bounded, cancellable per-page
subprocesses. Raster output is capped at 4096 pixels per side and 8 million pixels.
Native calls and Go image processing are separately serialized to bound memory.

Required native tools: `pdfinfo`, `pdftoppm`, `pdftotext`.
Optional annotation reader: `pdftohtml`. Packaged releases include all four.
Lookup order: `EASYREAD_PDF_BIN`, beside the executable, executable `bin/`, `PATH`.
`Dependencies()` reports availability and the extraction precision contract.

New page images and crops use PNG. All returned metadata references the actual
extension. Existing WebP page images can still be decoded for resized variants.
Each extraction writes `extract/page-NNN.txt` and `page-NNN.chars.json`; selectable
text also uses a source-stamped versioned cache. Rotations and nonzero CropBox
origins are covered by generated-PDF integration tests.

## Deliberate precision differences

- Poppler exposes word bounding boxes, not individual glyph metrics. Unicode
  codepoints are preserved; character boxes are interpolated within word bounds
  in the correct rotated reading direction. Ligatures and full-width forms are
  normalized for matching. This is not PDFium-equivalent glyph precision.
- Figure auto-crops detect visible non-text pixels above a matched caption.
  Existing explicit and manual crops take precedence. The detector abstains when
  a sufficiently sized graphic cannot be established; it is not a PDF vector
  object classifier.
- Annotation links are inserted only for one unambiguous literal label outside
  existing Markdown links, code, and math. No translated label is guessed.
- `TextOnlyPages` returns true only for proven all-white raster pages. Visible
  text or any graphic is retained, conservatively disabling the former
  continuation-page omission optimization for nonblank pages.

`go test ./pdf` runs pure-Go tests everywhere and native integration tests when
Poppler is installed. `go test -race ./pdf` exercises the same paths with race
instrumentation. The production backend is built with CGO disabled; Poppler is a
separate native dependency, not a Go C binding.

## Distribution validation

`scripts/build_backend.cjs` builds the CGO-free service and requires native PDF
utilities to be bundled. On macOS, `bundle_poppler.cjs` copies the Homebrew tools,
recursively relocates dylibs with `otool`/`install_name_tool`, verifies their target
architecture, and re-signs them before the app's existing signing step. It also
bundles `poppler-data`, rewrites its NUL-terminated compiled resource-path
constants in copied binaries without changing Mach-O offsets, and uses the bundle
working directory for those subprocesses. Fontconfig uses macOS system fonts and
a writable temporary cache. The bundler fails if resource relocation is not
possible rather than shipping a known host-dependent package.

Node tests simulate and inspect dylib/resource relocation; actual macOS loader,
CMap rendering and signing validation must run on the macOS CI runner. They were
not exercised on the Linux development environment. Other platforms require an
explicit complete `EASYREAD_PDF_BIN` distribution including shared libraries,
resources and licenses; merely compiling the Go binary does not bundle Poppler.
