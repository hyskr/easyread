# Native Go backend

This directory implements the EasyRead service in Go while retaining the existing HTTP API, frontend, and JSON library layout. The Python source remains in the repository as a compatibility reference and explicitly selectable legacy build; native launchers do not import or execute it.

## Build and run

Requirements: Go 1.23+, and Poppler tools `pdfinfo`, `pdftoppm`, `pdftotext`, and `pdftohtml` for PDF work. Install Poppler with your operating system package manager. All frontend assets are embedded at build time through the local `easyread/web` module.

```sh
cd backend
go build -trimpath -o ../build/backend/easyread-backend ./cmd/easyread
../build/backend/easyread-backend serve --port 8765 --open
```

Use `EASYREAD_HOME` for configuration/data and `EASYREAD_LIBRARY` for an isolated library. Existing Python-created libraries remain readable. Do not run different backend versions against the same library simultaneously. Back up important libraries before testing a major migration.

`EASYREAD_PDF_BIN` selects a directory containing the native PDF tools. Packaged builds search beside the executable and in its `bin` directory before PATH. `--web-root /absolute/path/easyread/web` enables frontend development without recompiling assets.

`easyread-backend help` lists native CLI commands. `GET /api/health` reports the backend and available PDF dependencies. The HTTP listener binds to loopback, validates Host/Origin, and requires the token from `/api/library` for writes.

## Release packaging

The existing `v*` Electron release workflow is explicitly retained as `dist:legacy` until native Windows/Linux PDF bundles have release validation. Wails releases use the Go build; Electron keeps its existing Python build.

`npm run build:backend:go` builds the Go executable and bundles native PDF dependencies. On macOS, it relocates the Poppler executables and their transitive libraries. On Windows/Linux, set `EASYREAD_PDF_BIN` to a complete relocatable distribution with its license files; the packager fails rather than silently building a nonfunctional PDF bundle. A development build can use system Poppler without bundling.

`./scripts/build_wails.sh` packages the macOS Wails app with the Go backend. No Python/PyInstaller is used. Node is still needed to build frontend assets. Native macOS signing and release packaging must be validated on macOS; Linux unit tests do not establish that a signed app works.

Poppler is separately licensed. Review the bundled notices and corresponding-source distribution obligations before publishing binaries. The bundle manifest is not itself a substitute for complying with those obligations.

## Optional translated PDF export

BabelDOC remains an optional third-party converter, installed separately. Set `EASYREAD_BABELDOC` to its executable if it is not on PATH. Go starts an authenticated, loopback-only OpenAI-compatible bridge and routes requests through the selected EasyRead model, including CLI subscriptions. The EasyRead Python adapter is not used. BabelDOC itself may require its own Python environment and downloaded models.

## Validation

```sh
cd backend
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
cd ..
node --test tests/web/test_*.cjs tests/desktop/test_*.cjs scripts/bundle_poppler.test.cjs
node tests/backend/native-smoke.cjs
```

Tests use temporary libraries, mock HTTP services and fake model executables. The PDF suite creates small real PDFs and invokes installed Poppler tools. Tests do not require paid model calls. The native CLI tests build and invoke an executable from a temporary directory.

See [the migration test map](../docs/go-migration-test-map.md) for original Python test scenarios and remaining coverage, and [PDF compatibility notes](pdf/README.md) for deliberate engine differences. Passing the new tests does **not** establish complete behavioral equivalence with the original Python suite.

## Known differences and unverified integrations

- Poppler supplies word boxes. Character boxes are interpolated, not PDFium glyph metrics. Native figure geometry and some complex cross-column layouts use different algorithms; see the PDF notes.
- Text-bearing empty continuation output is conservatively retried/rejected when complete source coverage cannot be proven. This can produce partial jobs where the former implementation accepted a continuation.
- Exact original prompt wording, local CLI version fallbacks, and publisher responses are not guaranteed byte-for-byte equivalents. Live paid model and publisher integration tests have not been performed.
- Windows subprocess cancellation and signed macOS application behavior need native-platform verification.
- The historical Python suite has not been fully ported. Its test map distinguishes partial and unported cases; it is not a numerical parity claim.
