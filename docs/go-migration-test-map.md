# Go migration test mapping

Snapshot: 2026-11-03 06:48 UTC, expanded regression round. This is a test-evidence inventory, not a feature-completeness certificate.

## Counts and limits

- Original Python source inventory: **83 test files, 620 `test*` methods/functions** (Python AST enumeration).
- Current Go source inventory: **33 test files, 217 top-level `TestXxx(*testing.T)` functions**. `TestMain` is a harness, excluded. Table rows/subtests are not counted as separate top-level functions.
- `go test -list . ./...` was run from `backend`. Repeated discovery was used because tests were added concurrently; the latest source inventory above supersedes earlier 85/110/111 counts. Listing tests does not run their assertions.
- **284 JavaScript tests passed (282 existing plus 2 packaging tests)**. They exercise retained frontend behavior, not migrated Go backend equivalence. `tests/backend/native-smoke.cjs` passed against the compiled backend with Go/Python/Node absent from its child PATH, covering real PDF preparation, embedded assets, note persistence and offline export. The original Python run was incomplete (467 successes and one i18n failure in the partial log); this is not a green baseline. See [final validation](go-migration-validation.md) for commands and limitations.
- Conservative file-level classification: **12 directly ported files, 71 partial/related files, 0 files with no corresponding Go test**. “Partial” does not mean a percentage of the original methods passed. Some related smoke tests assert less than even one original method.
- No method-level equivalence percentage is claimed. A Go test can combine multiple assertions, while one original method may have dozens of cases. Passing the new Go functions cannot substitute for porting 620 original methods.
- Go integration fixtures use local mock HTTP servers/fake CLIs and small synthetic PDFs. No paid model calls or real provider authentication were exercised. PDF tests can skip when Poppler is unavailable; a listed or package-passing test is not proof that every optional dependency case ran.

## Platform execution limits

- POSIX fake-CLI tests in engines, chat and BabelDOC suites explicitly skip on Windows; helper-based PDF subprocess tests are portable. Linux execution counts/skips are recorded in the validation report. Windows cross-compilation is not runtime verification.

## Reading the map

- **Covered scenario** means the specific assertion named below has a direct Go counterpart; it does not certify the entire old method/file.
- **Partial** means named tests exercise a related subset, with explicit untested original scenarios.
- **Not yet ported** means no matching Go test was found in the inspected snapshot, even when implementation code exists.
- Links identify exact new Go source files; named functions are searchable in those files. Original method names are enumerated in the appendix so gaps remain actionable.

## Examples of directly covered assertions (not whole-file equivalence)

| Original assertion | Go test evidence | Limit |
|---|---|---|
| `app/test_api_keys.py`: changing custom API hosts must not transfer a key | `TestSaveMasksAndScopesKeys`, `TestKeyIsolation`, `TestEngineConfigDoesNotShareKey` | Image-service and legacy migration cases still missing. |
| `library/test_unreadable_data.py`: an unreadable reader cannot be overwritten by an edit | `TestCorruptProtection`, `TestHTTPCorruptReaderPreserved` | Damaged trash/recovery matrix separate. |
| `library/test_reader_ops.py`: delayed creation cannot resurrect a deleted note | `TestReaderOpsAndHistory` | Only deletion-first ordering asserted. |
| `engines/test_openai_api.py`: Responses completion and streaming output | `TestChatAndResponses/responses`, `TestStreaming/responses` | Retries and ignored-stream behavior separate. |
| `engines/test_openai_api.py`: cancellation does not wait for the provider response | `TestAPICancellation` | No backoff cancellation matrix. |
| `engines/test_api_stream_errors.py`: unexpected SSE EOF must not count as success | `TestStreamIncomplete` | One chat event framing fixture. |
| `library/test_cloudlib.py`: copied source, notes, trash remain intact | `TestLocationCopyPreservesSourceTrashAndUnknown` | Fault-injected publication/rollback variants missing. |
| `translate/test_readmode.py`: fill existing English block in place | `TestPagesCheckpointRetryAndFill` | Multi-page/read-only combinations missing. |
| `translate/test_translate.py`: canceled model result must not publish blocks | `TestCancellationDoesNotPublish` | Queue cancellation is a separate contract. |

## Original file → Go test evidence

| Original file (methods) | Status | Exact Go tests | Untested original cases / scope limit |
|---|---|---|---|
| `tests/app/test_api_keys.py` (4) | Partial | [`TestSaveMasksAndScopesKeys`](../backend/config/config_test.go)<br>[`TestKeyIsolation`](../backend/engines/engines_test.go)<br>[`TestEngineConfigDoesNotShareKey`](../backend/engines/engines_test.go)<br>[`TestHTTPSettingsDoNotLeakOrMoveLibrary`](../backend/server/server_test.go) | Endpoint isolation/public masking checked; image-service settings validation and legacy unnamed-key migration lack dedicated equivalents. |
| `tests/app/test_folder_pick.py` (5) | Covered (original mocked assertions) | [`TestFolderPickChosenPathAndEnvironment`](../backend/server/folder_picker_test.go)<br>[`TestFolderPickCancelReturnsEmpty`](../backend/server/folder_picker_test.go)<br>[`TestFolderPickWindowsFailureFallsBack`](../backend/server/folder_picker_test.go)<br>[`TestFolderPickUnavailable`](../backend/server/folder_picker_test.go)<br>[`TestFolderPickPowerShellASCIIAndExplorerStyle`](../backend/server/folder_picker_test.go) | All five original mocked cases directly ported. Additional Linux/macOS/context fixtures do not certify real native dialogs on those operating systems. |
| `tests/app/test_i18n.py` (7) | Partial | [`TestLocalizedRealReader`](../backend/exporter/export_test.go) | Export language injection only; Electron/environment precedence, complete dictionaries and Traditional Chinese injection not ported. |
| `tests/app/test_open_link.py` (5) | Partial | [`TestOpenLinksIDHashAndSafeAnchor`](../backend/server/updates_test.go)<br>[`TestHTTPWireContracts`](../backend/server/server_test.go) | ID/hash/anchor redirects, unsafe anchors, malformed/unknown fallback now tested; version smoke does not explicitly assert every original version field. |
| `tests/app/test_release_i18n.py` (2) | Covered (direct assertion counterparts) | [`TestReleaseEnglishDictionaryHasNoDuplicateKeys`](../backend/config/release_i18n_test.go)<br>[`TestReleaseFeatureTranslationsPreserveNamedParameters`](../backend/config/release_i18n_test.go) | Both assertions check the real embedded English dictionary and seven original frontend files; no claim that every untranslated backend diagnostic is localized. |
| `tests/app/test_updates.py` (5) | Partial | [`TestVersionOrdering`](../backend/server/updates_test.go)<br>[`TestUpdateThrottleDisableForceFailure`](../backend/server/updates_test.go) | Version comparisons, disabled no-call, cached throttle and forced HTTP failure tested; exact offline-network and 24-hour boundary behavior remains partial. |
| `tests/bulk/test_folder.py` (3) | Partial | [`TestFolderDeduplication`](../backend/importer/importer_test.go) | Nested folder category and duplicate import basics; missing folder and combining categories from distinct paths not asserted. |
| `tests/bulk/test_runner.py` (8) | Partial | [`TestFolderDeduplication`](../backend/importer/importer_test.go)<br>[`TestLinkedImportDoesNotPrepare`](../backend/importer/importer_test.go)<br>[`TestZoteroReadOnly`](../backend/importer/importer_test.go) | Scan/start/dedup and deferred linked preparation only; download failure continuation, tag/date switches, category clipping and linked-network rejection not fully tested. |
| `tests/chat/test_answer_styles.py` (13) | Partial | [`TestCLIImagesAndColorFilteredContext`](../backend/chat/chat_test.go)<br>[`TestNDJSONStoresAnswerAndImages`](../backend/chat/chat_test.go) | Selective annotation context and successful stream only; all 13 mode-specific contracts, bounded full-paper evidence, formulas, mode persistence and invalid-mode HTTP are not established. |
| `tests/chat/test_chat.py` (4) | Partial | [`TestLegacyThreadsAndMutation`](../backend/chat/chat_test.go)<br>[`TestCLIImagesAndColorFilteredContext`](../backend/chat/chat_test.go)<br>[`TestEngineConfigDoesNotShareKey`](../backend/engines/engines_test.go) | Legacy chat and marks-on-request overlap; no think-tag stripping test, and preset-key selection differs from custom-host isolation fixture. |
| `tests/chat/test_chat_effort.py` (2) | Partial | [`TestInvalidModelSpecificEffortBeforeStreaming`](../backend/chat/chat_test.go) | Invalid Haiku effort rejected before streaming; successful conversation override and card/translation immutability not asserted. |
| `tests/chat/test_chat_images.py` (6) | Partial | [`TestImageValidationAndPaths`](../backend/chat/chat_test.go)<br>[`TestNDJSONStoresAnswerAndImages`](../backend/chat/chat_test.go)<br>[`TestFailureCleansAttachments`](../backend/chat/chat_test.go)<br>[`TestCLIImagesAndColorFilteredContext`](../backend/chat/chat_test.go) | Valid PNG save/stream, malformed-image atomicity and symlink defense; full type/count/byte/pixel limits, last-four history selection, actual CLI argv attachments and routed HTTP follow-up serving not fully asserted. |
| `tests/chat/test_chat_lean.py` (5) | Covered (direct assertion counterparts) | [`TestClaudeChatReadOnlyToolsAndChoices`](../backend/chat/lean_parity_test.go)<br>[`TestClaudeUnsupportedToolsFallbackOnlyBeforeOutput`](../backend/engines/lean_test.go)<br>[`TestClaudeChatLeavesCallerContextActive`](../backend/chat/lean_parity_test.go)<br>[`TestCodexChatAnswerRetranslateLeanIsolation`](../backend/chat/lean_parity_test.go) | All five source assertions mapped; local POSIX fake CLIs exercise actual chat/Answer/Retranslate paths. Windows fake-shell execution and authenticated providers remain untested. |
| `tests/chat/test_chat_store.py` (2) | Partial | [`TestLegacyThreadsAndMutation`](../backend/chat/chat_test.go) | Rename/delete legacy thread and pin basics; empty creation and frozen-clock ID collisions not tested. |
| `tests/engines/test_agent_cli.py` (15) | Partial | [`TestEveryCLIAdapter`](../backend/engines/engines_test.go)<br>[`TestCLI`](../backend/engines/engines_test.go)<br>[`TestCLICancelProcessGroup`](../backend/engines/engines_test.go) | All adapters parse one fake happy-path result; stdin/argv formats, pretty JSON, default executable selection, agent-name collision, all error and effort cases not individually asserted. |
| `tests/engines/test_api_stream_errors.py` (13) | Partial | [`TestThinkTagsEveryChunkBoundary`](../backend/engines/stream_parity_test.go)<br>[`TestUnclosedThinkHiddenPlainAnglesRemain`](../backend/engines/stream_parity_test.go)<br>[`TestSSEFramingAndInvalidEvents`](../backend/engines/stream_parity_test.go)<br>[`TestSSETerminalFailuresNoRetry`](../backend/engines/stream_parity_test.go)<br>[`TestStreaming`](../backend/engines/engines_test.go)<br>[`TestStreamIncomplete`](../backend/engines/engines_test.go)<br>[`TestResponsesIncomplete`](../backend/engines/engines_test.go)<br>[`TestAPICancellation`](../backend/engines/engines_test.go) | Think-tag byte/character boundaries, unclosed tags, comments/multiline/relay framing, null/malformed events, terminal error variants and cancellation tested. Retry-After date/seconds parsing and long rate-limit backoff are not ported. |
| `tests/engines/test_chat_tools.py` (4) | Partial | [`TestSafeExtraArguments`](../backend/engines/engines_test.go) | Rejecting permission overrides is tested; chat-only web research/tool allowlists and translation isolation are not directly asserted. |
| `tests/engines/test_codebuddy_cli.py` (6) | Partial | [`TestEveryCLIAdapter`](../backend/engines/engines_test.go)<br>[`TestSafeExtraArguments`](../backend/engines/engines_test.go)<br>[`TestCLICancelProcessGroup`](../backend/engines/engines_test.go) | Basic result and safe-argument validation only; base64 stdin image shape, region inheritance, usage, error variants and CodeBuddy-specific cancellation not established. |
| `tests/engines/test_codex_image.py` (9) | Partial | [`TestCodexImageSubscriptionAuthAndCleanup`](../backend/engines/image_test.go)<br>[`TestCodexImageNoToolResultNoRetry`](../backend/engines/image_test.go)<br>[`TestCodexFreshGeneratedArtifactImported`](../backend/engines/image_test.go)<br>[`TestCodexArtifactScopeFreshnessAndSymlinks`](../backend/engines/image_test.go)<br>[`TestCodexImageInvalidOrSymlinkResultRejected`](../backend/engines/image_test.go)<br>[`TestCodexImageCancellationTimeoutAndPreCancelled`](../backend/engines/image_test.go) | Fake executable tests cover auth/env, fresh/old/outside paths, file/directory symlinks, valid home alias, timeout/cancel/cleanup and no retry. Python meter usage assertion has no ImageEdit usage-return equivalent; output is PNG rather than WEBP. Windows shell fixtures skip; Windows child-tree cancellation is cross-compiled, not executed here. |
| `tests/engines/test_codex_translation_options.py` (7) | Partial | [`TestSafeExtraArguments`](../backend/engines/engines_test.go)<br>[`TestEngineConfigDoesNotShareKey`](../backend/engines/engines_test.go) | Safe Codex config args and no cross-host key mutation only; cache persistence, card effort/tier, CLI default and API effort-body matrix not asserted. |
| `tests/engines/test_engine_lean.py` (11) | Partial | [`TestCLI`](../backend/engines/engines_test.go)<br>[`TestSafeExtraArguments`](../backend/engines/engines_test.go) | Safe CLI invocation basics only; MCP config parsing and unknown-option fallback matrix not ported. |
| `tests/engines/test_image_edit.py` (5) | Partial | [`TestImageEditMultipartActualImage`](../backend/engines/image_test.go)<br>[`TestImageSignedDownloadNoCredentials`](../backend/engines/image_test.go)<br>[`TestImageEditRejectsInvalidOutputs`](../backend/engines/image_test.go)<br>[`TestImageHTTPFailureNoRetryOrLeak`](../backend/engines/image_test.go)<br>[`TestImageCancellationDiscardsLateResponse`](../backend/engines/image_test.go)<br>[`TestImagePreCancelledAndInvalidSourceNoRequest`](../backend/engines/image_test.go) | All five original behavioral scenarios now exercised with real local multipart/HTTP requests, plus MIME/pre-cancel checks. Deliberate representation difference: Go returns normalized PNG, Python asserted WEBP. No paid service used. |
| `tests/engines/test_netcheck.py` (5) | Covered (direct assertion counterparts) | [`TestNetworkAnyHTTPResponseAndCache`](../backend/engines/netcheck_test.go)<br>[`TestNetworkUnreachableLocal`](../backend/engines/netcheck_test.go)<br>[`TestNetworkOverseasPreset`](../backend/engines/netcheck_test.go)<br>[`TestNetworkExplainOnlyNetworkErrors`](../backend/engines/netcheck_test.go)<br>[`TestNetworkCertificateNoVPNOrRetry`](../backend/engines/netcheck_test.go) | All five source assertions mapped with local services/errors. Checked chat/selection/translation paths now invoke preflight. OS system-proxy import remains unimplemented and is outside these five original tests. |
| `tests/engines/test_openai_api.py` (7) | Partial | [`TestChatAndResponses`](../backend/engines/engines_test.go)<br>[`TestStreaming`](../backend/engines/engines_test.go)<br>[`TestAPICancellation`](../backend/engines/engines_test.go) | Both API body/result/usage basics and cancellation tested; rejected temperature, model listing, ignored-stream fallback and whole-stream complete behavior are not asserted. |
| `tests/engines/test_opencode_runtime.py` (3) | Partial | [`TestEveryCLIAdapter`](../backend/engines/engines_test.go) | Parses fake OpenCode text; no assertions for runtime environment, argv, retained provider login or cleanup ordering. |
| `tests/engines/test_parse_json.py` (10) | Partial | [`TestJSONNormalizeAndSentences`](../backend/translate/translate_test.go) | Single fenced JSON example only; nested/truncated JSON, escaped TeX, raw newlines, citation prose and empty structures not ported. |
| `tests/engines/test_qoder_cli.py` (5) | Partial | [`TestEveryCLIAdapter`](../backend/engines/engines_test.go)<br>[`TestSafeExtraArguments`](../backend/engines/engines_test.go) | Happy result plus generic restrictions; CN executable resolution, chat permissions, failures, model config mutation matrix absent. |
| `tests/engines/test_stream_recovery.py` (5) | Partial | [`TestExplicitStreamRejectionFallsBackOnce`](../backend/engines/stream_parity_test.go)<br>[`TestAuthFailureNeverUsesStreamFallback`](../backend/engines/stream_parity_test.go)<br>[`TestSSETerminalFailuresNoRetry`](../backend/engines/stream_parity_test.go) | Explicit HTTP 400 unsupported-stream falls back once for both APIs. Auth/output-limit failures do not retry. Intentional billing-safety difference: interrupted streams are not automatically replayed, so original disconnect-retry/backoff tests are NOT equivalent. |
| `tests/engines/test_usage.py` (5) | Partial | [`TestChatAndResponses`](../backend/engines/engines_test.go)<br>[`TestStreaming`](../backend/engines/engines_test.go)<br>[`TestCLI`](../backend/engines/engines_test.go) | Simple API/Claude token totals only; cache inclusion, subscription limits vs paid costs, DeepSeek and meter merge not ported. |
| `tests/engines/test_zcode_cli.py` (6) | Partial | [`TestEveryCLIAdapter`](../backend/engines/engines_test.go) | Happy response parsing only; long Unicode task attachments, runtime isolation, usage/error/projection/cancel/detection matrix absent. |
| `tests/export/test_mobile_export.py` (3) | Partial | [`TestRealReaderOfflineAndMobile`](../backend/exporter/export_test.go)<br>[`TestHTMLInlinesAssetsAndEscapesData`](../backend/exporter/export_test.go) | Real reader assets bundled and mobile marker injected; translated-page omission, note retention and translated-image original/text fallback not asserted. |
| `tests/library/test_cloudlib.py` (27) | Partial | [`TestLocationCopyPreservesSourceTrashAndUnknown`](../backend/server/library_location_test.go)<br>[`TestLocationMergeSkipsSamePDFAndCollision`](../backend/server/library_location_test.go)<br>[`TestLocationFailureRollsBackOnCorruptConfig`](../backend/server/library_location_test.go)<br>[`TestLocationSymlinkRejected`](../backend/server/library_location_test.go)<br>[`TestLocationBusyAndNestedBlocked`](../backend/server/library_location_test.go)<br>[`TestIncompleteCleanupConfinedToTarget`](../backend/server/library_location_test.go)<br>[`TestRemoteAndReadyMarkersRemainUsable`](../backend/server/library_location_test.go)<br>[`TestHTTPRestartReadOnlyGate`](../backend/server/server_test.go) | Several migration happy/error paths tested; 27-case fault injection (second publication, locked rollback, cleanup failure), use mode, slow/unavailable drive, live concurrency gate and source-change races remain unported. |
| `tests/library/test_cloudlib_detect.py` (7) | Partial | [`TestLocationBusyAndNestedBlocked`](../backend/server/library_location_test.go)<br>[`TestIncompleteCleanupConfinedToTarget`](../backend/server/library_location_test.go) | Nested-target and marker behavior overlap only; Windows iCloud spellings, custom empty/current/file directory normalization matrix absent. |
| `tests/library/test_fs_retry.py` (4) | Covered (direct assertion counterparts) | [`TestRetryWriteTransientInvalidArgumentRecovers`](../backend/store/fs_retry_test.go)<br>[`TestRetryWritePersistentErrorIsBounded`](../backend/store/fs_retry_test.go)<br>[`TestExistingLockNeverBypassed`](../backend/store/fs_retry_test.go)<br>[`TestLockWriteFailureClosesDescriptorAndRemovesOwnedLock`](../backend/store/fs_retry_test.go) | All four source assertions plus ownership/retry safety cases mapped. Windows error classification is fault-injected on Linux; real Windows filesystem filter drivers were not run. |
| `tests/library/test_linked_pdf.py` (10) | Partial | [`TestImportLinksTrashAndRestore`](../backend/store/store_test.go)<br>[`TestConvertPDFToLinkRequiresEqualCopyAndConsent`](../backend/store/store_test.go)<br>[`TestLinkedImportDoesNotPrepare`](../backend/importer/importer_test.go) | External source, duplicate preservation, trash/restore, metadata source spoofing and equal-copy consent basics; linked render/image pipeline, missing-source HTTP and cloud copy matrix absent. |
| `tests/library/test_reader_ops.py` (2) | Covered (original mocked assertions) | [`TestReaderDeletionWinsDelayedCreationBothOrders`](../backend/store/store_test.go)<br>[`TestReaderReadingPositionOffsetRatioAndDelayedProgress`](../backend/store/store_test.go) | Both original methods directly ported: both operation orders, explicit newer restoration, complete position tuple, stale update rejection, and legacy offset removal. |
| `tests/library/test_reader_preparation.py` (10) | Partial | [`TestHTTPImportAndPrepareRealPDF`](../backend/server/server_test.go)<br>[`TestCancellation`](../backend/pdf/pdf_test.go) | Successful queued preparation and pre-cancel only; nonblocking read response, dedup, cross-paper serialization, thread-start failures, queue transition races and duplicate response protection not fully tested. |
| `tests/library/test_trash.py` (6) | Partial | [`TestImportLinksTrashAndRestore`](../backend/store/store_test.go) | Single trash/restore and traversal rejection; newest-first ordering, restore conflict, read-only purge and sync-lock retries not ported. |
| `tests/library/test_unreadable_data.py` (4) | Partial | [`TestUnreadableJobPreserved`](../backend/jobs/jobs_test.go)<br>[`TestCorruptProtection`](../backend/store/store_test.go)<br>[`TestListIncludesCorruptEntries`](../backend/store/store_test.go)<br>[`TestHTTPCorruptReaderPreserved`](../backend/server/server_test.go) | Corrupt job/reader refusal and healthy listing tested; healthy queued-job resume alongside damage and damaged trash restore not asserted. |
| `tests/pdf/test_blank_footnotes.py` (2) | Partial | [`TestAutomaticFigureAndConservativeBlankPages`](../backend/pdf/pdf_test.go) | Blank raster is accepted and colored graphic rejected; blank-page no-model-call decision and existing-content/source-missing branches not exercised. |
| `tests/pdf/test_figure_geometry.py` (31) | Partial | [`TestManualCropResetAndLayout`](../backend/pdf/pdf_test.go)<br>[`TestRotatedCropBox`](../backend/pdf/pdf_test.go)<br>[`TestAutomaticFigureAndConservativeBlankPages`](../backend/pdf/pdf_test.go) | Manual crop, rotations and one synthetic rectangle automatic crop tested; 31-case panels/logos/captions/forms/clipping/header-boundary geometry suite mostly unported. |
| `tests/pdf/test_figures.py` (22) | Partial | [`TestManualCropResetAndLayout`](../backend/pdf/pdf_test.go)<br>[`TestPrepareCropAndSelection`](../backend/pdf/pdf_test.go)<br>[`TestDerivedDirectorySymlinkIsRejected`](../backend/pdf/pdf_test.go) | Manual crop/reset, basic crop bounds and external output symlink rejected; stale auto-crops, dedup, reference-aware cleanup, locks and concurrent edits matrix absent. |
| `tests/pdf/test_inline_links.py` (3) | Partial | [`TestAnnotationLinks`](../backend/pdf/pdf_test.go) | Real annotation extraction, safe URL and code-span checks; existing-paper backfill preservation and ambiguous-match fixtures missing. |
| `tests/pdf/test_multiregion_layout.py` (6) | Partial | [`TestColumnRegionsKeepGap`](../backend/pdf/pdf_test.go)<br>[`TestCrossPageLocations`](../backend/pdf/pdf_test.go) | Synthetic two-column gap and cross-page regions; three columns, fragment clamping/formula/caption and stale-layout upgrade matrix absent. |
| `tests/pdf/test_paragraph_regions.py` (5) | Partial | [`TestCrossPageLocations`](../backend/pdf/pdf_test.go)<br>[`TestColumnRegionsKeepGap`](../backend/pdf/pdf_test.go) | Basic region splitting only; prose-vs-axis-label and truncated cross-column crop boundaries not tested. |
| `tests/pdf/test_pdf_concurrency.py` (2) | Covered (direct assertion counterparts) | [`TestNativeRenderExtractCropAndModelImagesCannotOverlap`](../backend/pdf/concurrency_test.go)<br>[`TestNativeProcessesCloseAfterModelImageSaveError`](../backend/pdf/concurrency_test.go) | Both scenarios have native counterparts: process execution/reaping is serialized and save failures clean up. Go verifies child-process lifecycle instead of PDFium handle objects. Tests use authored child helpers, not actual PDFium. |
| `tests/pdf/test_pdf_extraction.py` (6) | Partial | [`TestRotatedCropBox`](../backend/pdf/pdf_test.go)<br>[`TestUnicodeRowsAndNormalization`](../backend/pdf/pdf_test.go)<br>[`TestRotatedGlyphNormalization`](../backend/pdf/pdf_test.go) | Real rotated CropBox and synthetic Unicode rows; shared long forms, true ligature glyph metrics, partial glyph clipping, write-error handle cleanup and large-stream memory behavior absent. |
| `tests/pdf/test_pdfwork.py` (11) | Partial | [`TestPrepareCropAndSelection`](../backend/pdf/pdf_test.go)<br>[`TestRotatedCropBox`](../backend/pdf/pdf_test.go)<br>[`TestManualCropResetAndLayout`](../backend/pdf/pdf_test.go)<br>[`TestSymlinkVariantEscape`](../backend/pdf/pdf_test.go) | Basic variant, crop/layout, rotation and symlink behavior; small-image pass-through, warming, all caption/formula/heading geometry and movable directory checks not ported. |
| `tests/pdf/test_prepare_worker.py` (4) | Partial | [`TestHTTPImportAndPrepareRealPDF`](../backend/server/server_test.go)<br>[`TestCancellation`](../backend/pdf/pdf_test.go) | Real Poppler preparation and cancellation only; OOM lower-resolution retry, repeated OOM and unrelated errors not ported. |
| `tests/pdf/test_render_limits.py` (8) | Partial | [`TestRenderLimits`](../backend/pdf/pdf_test.go)<br>[`TestRotatedCropBox`](../backend/pdf/pdf_test.go) | Numeric pixel/side cap checked; real huge-page render, ordinary scale, invalid dimensions and native memory-error retries not asserted. |
| `tests/pdf/test_selectable_text.py` (3) | Partial | [`TestPrepareCropAndSelection`](../backend/pdf/pdf_test.go) | Real character output and invalid page tested; whitespace fidelity/cache reuse/source invalidation not asserted. |
| `tests/pdf/test_sources.py` (2) | Partial | [`TestFetchCitation`](../backend/importer/importer_test.go) | Citation title apostrophe and metadata extraction covered on one HTML fixture; full quote/entity/attribute-order cases not ported. |
| `tests/pdf/test_text_regions.py` (3) | Partial | [`TestCrossPageLocations`](../backend/pdf/pdf_test.go)<br>[`TestColumnRegionsKeepGap`](../backend/pdf/pdf_test.go) | Simple cross-page/two-column splitting; footer/label exclusion, reading-order and unmatched-paragraph cases not established. |
| `tests/server/test_babeldoc_engine.py` (4) | Partial | [`TestPDFExportFakeExecutable`](../backend/server/pdf_export_test.go)<br>[`TestPDFBridgeFakeEngine`](../backend/server/pdf_export_test.go)<br>[`TestPDFCopyNoOverwrite`](../backend/server/pdf_export_test.go)<br>[`TestPDFRestartStatus`](../backend/server/pdf_export_test.go)<br>[`TestPDFBridgeAuthentication`](../backend/server/pdf_export_test.go) | Fake CLI output and authenticated bridge/no-overwrite/restart tested; destination validation before model work, save failure download retention and selected-Codex no-key path not fully tested. |
| `tests/server/test_figure_jobs.py` (8) | Partial | [`TestRestartResumesTextButNeverInFlightImage`](../backend/jobs/jobs_test.go)<br>[`TestImageGenerationReuseAndInputIdentity`](../backend/translate/figures_test.go)<br>[`TestImageLateResultCannotOverwriteEdits`](../backend/translate/figures_test.go)<br>[`TestImageCancelAfterResponseDoesNotPublish`](../backend/translate/figures_test.go) | Restart replay refusal and direct pipeline stale-result/cancel checks; actual queued HTTP image jobs, toggle/selection/duplicate/force-regeneration matrix still not established. |
| `tests/server/test_http.py` (3) | Covered (direct assertion counterparts) | [`TestOutboundTLSUntrustedCertificateRejected`](../backend/engines/http_test.go)<br>[`TestOutboundTLSExplicitTrustedCertificateUsed`](../backend/engines/http_test.go)<br>[`TestOutboundTLSHostnameMismatchRejected`](../backend/engines/http_test.go) | All three original outbound trust/hostname scenarios directly exercised with local TLS certificates. Go uses OS roots plus SSL_CERT_FILE/DIR, not a bundled Python certifi store; no public TLS endpoint used. |
| `tests/server/test_import_retry.py` (2) | Partial | [`TestHTTPImportAndPrepareRealPDF`](../backend/server/server_test.go) | Successful first import only; retry after preparation failure, active/prepared entry suppression not asserted. |
| `tests/server/test_job_queue_recovery.py` (9) | Covered (direct assertion counterparts) | [`TestDeleteDuringPreparationKeepsExistingAndNewJobsRunning`](../backend/jobs/recovery_edges_test.go)<br>[`TestDeleteDuringTranslationAndUsageFlushKeepsQueueRunning`](../backend/jobs/recovery_edges_test.go)<br>[`TestDeleteDuringRunningStateWriteKeepsQueueRunning`](../backend/jobs/recovery_edges_test.go)<br>[`TestStatusWriteFailuresDoNotKillWorker`](../backend/jobs/recovery_edges_test.go)<br>[`TestExistingWorkspaceWriteErrorsAreNotSilenced`](../backend/jobs/recovery_edges_test.go) | All nine original deletion/write-failure assertions mapped using production worker recovery with injected local runners/persistence faults. Restart checkpoint and corrupt-job tests remain separately included. |
| `tests/server/test_jobs.py` (7) | Partial | [`TestCancelAndIndependentSmallQueue`](../backend/jobs/jobs_test.go)<br>[`TestRestartResumesTextButNeverInFlightImage`](../backend/jobs/jobs_test.go) | Running cancellation, independent small queue, restart resume basics; duplicate enqueue, cancellation-before-start, worker start ordering, frozen-clock IDs and deleted-small-job cleanup absent. |
| `tests/server/test_page_cap.py` (21) | Partial | [`TestCapConfirmAndTargetRetention`](../backend/jobs/jobs_test.go)<br>[`TestHTTPSettingsDoNotLeakOrMoveLibrary`](../backend/server/server_test.go) | Three-page fixture cap, JSON-true confirmation and saved target/instructions; remaining 21-case ranges/structured pages/zero cap/resume/nonblocking matrix not ported. |
| `tests/server/test_parallel_papers.py` (6) | Covered (direct assertion counterparts) | [`TestPaperWorkerCountBoundsAndStartsRequestedWorkers`](../backend/jobs/parallel_papers_test.go)<br>[`TestPaperConcurrencyPersistsInExistingConfig`](../backend/jobs/parallel_papers_test.go)<br>[`TestSettingsRejectOutOfRangeOrNonIntegerPaperCount`](../backend/jobs/parallel_settings_test.go)<br>[`TestSettingsSaveAllowedPaperCounts`](../backend/jobs/parallel_settings_test.go)<br>[`TestTwoPapersRunTogetherAndThirdWaits`](../backend/jobs/parallel_papers_test.go)<br>[`TestFinishingOldTaskPreservesRequeuedCancelHandle`](../backend/jobs/parallel_papers_test.go) | All six assertions mapped using gated local runners plus actual settings handlers. Additional old-run error/cancel-slot tests included. No real model service is used. |
| `tests/server/test_pdf_text.py` (3) | Partial | [`TestPrepareCropAndSelection`](../backend/pdf/pdf_test.go)<br>[`TestHTTPTraversalAndEmbeddedAssets`](../backend/server/server_test.go) | Direct text generation and generic asset containment; character Content-Type and selection=2 routed HTTP/cache separation not asserted. |
| `tests/server/test_prepare_retry.py` (1) | Partial | [`TestHTTPImportAndPrepareRealPDF`](../backend/server/server_test.go) | Successful preparation is not failure/retry-without-model equivalence; original scenario remains unported. |
| `tests/server/test_presence.py` (6) | Covered (direct assertion counterparts) | [`TestPresenceWaitsLongerBeforeFirstPage`](../backend/server/presence_test.go)<br>[`TestPresenceGraceAfterLastPageCloses`](../backend/server/presence_test.go)<br>[`TestPresenceBusyBlocksExit`](../backend/server/presence_test.go)<br>[`TestPresenceServerExitsAfterLastPageCloses`](../backend/server/presence_test.go)<br>[`TestPresenceServerRejectsOtherSite`](../backend/server/presence_test.go)<br>[`TestPresenceKeepTurnsOffAutoExit`](../backend/server/presence_test.go) | All six cases mapped with real loopback websocket connections and deterministic clock ticks; added quick-connect, protocol, token, background-close and exact default-boundary tests. |
| `tests/server/test_selection_translate.py` (7) | Covered (direct assertion counterparts) | [`TestSelectionContextAndReadOnly`](../backend/server/selection_parity_test.go)<br>[`TestSelectionCachesPhonetics`](../backend/server/selection_parity_test.go)<br>[`TestSelectionInvalidatesContextAndModel`](../backend/server/selection_parity_test.go)<br>[`TestSelectionLRUEviction`](../backend/server/selection_parity_test.go)<br>[`TestSelectionValidationAndCrossSiteNoModel`](../backend/server/selection_parity_test.go)<br>[`TestSelectionProviderFailureAndEmptyRetried`](../backend/server/selection_parity_test.go)<br>[`TestSelectionLongMultilineNotTruncated`](../backend/server/selection_parity_test.go) | All seven original HTTP cases mapped: source context, no write, phonetics cache, invalidation/LRU, bad requests/cross-origin, retryable failures and long selection. Additional caption/list context and failed-preflight cases covered. |
| `tests/server/test_stop_jobs.py` (8) | Partial | [`TestCancelAndIndependentSmallQueue`](../backend/jobs/jobs_test.go) | Single running cancellation only; queued unprepared/prepared, confirmation-pending, prepare-only, filtered IDs and HTTP malformed IDs absent. |
| `tests/test_issue_preview.py` (6) | Partial | [`TestManualCropResetAndLayout`](../backend/pdf/pdf_test.go)<br>[`TestMarkdownBundlesImagesAndRejectsTraversal`](../backend/exporter/export_test.go)<br>[`TestEveryCLIAdapter`](../backend/engines/engines_test.go) | Manual reset, export scoping and basic OpenCode result overlap; figure preference, invalid-page prewrite and tool-event/error/image argv specifics absent. |
| `tests/translate/test_code_blocks.py` (5) | Partial | [`TestCodeAliasesAndMathMarkers`](../backend/translate/parity_test.go)<br>[`TestTermsRespectOtherMeaningsAndCode`](../backend/translate/parity_test.go) | Listing alias and embedded fence/norm markers plus glossary code protection tested; direct-merge idempotence, both-page save and missing-code/unknown-type rejection matrix not fully ported. |
| `tests/translate/test_consistency.py` (17) | Partial | [`TestMergePreservesConcurrentChangesAndReader`](../backend/translate/translate_test.go)<br>[`TestTermsRespectOtherMeaningsAndCode`](../backend/translate/parity_test.go)<br>[`TestConsistencyCannotOverwriteReaderEdits`](../backend/translate/parity_test.go) | Concurrent reader preservation and one conflicting-term/code fixture covered; opening outline, previous-page references, majority rendering, count-matched replacement/alignment and no-call/cancel matrix absent. |
| `tests/translate/test_continuation.py` (26) | Partial | [`TestRepairDoneLeavesCrossPageContinuation`](../backend/store/store_test.go)<br>[`TestContinuationRequiresVerifiedSourceAndExactSuffix`](../backend/translate/parity_test.go) | Exact mathematical suffix and source-verifier refusal tested with an injected verifier. Production text-bearing continuation acceptance is not proven; 26 multi-page/read-only/parallel-seam scenarios remain mostly unported. |
| `tests/translate/test_figure_images.py` (12) | Partial | [`TestImageGenerationReuseAndInputIdentity`](../backend/translate/figures_test.go)<br>[`TestImageLateResultCannotOverwriteEdits`](../backend/translate/figures_test.go)<br>[`TestImageCancelAfterResponseDoesNotPublish`](../backend/translate/figures_test.go)<br>[`TestImageFailureDoesNotFallbackToPaidAPI`](../backend/translate/figures_test.go) | Mock image pipeline verifies source/text retention, language identity, reuse/missing asset, late edit/cancel and no paid fallback. Disabled/read-only modes, multi-figure failure continuation, actual image formats and every recrop/target race remain unported. |
| `tests/translate/test_float_order.py` (8) | Partial | [`TestFloatOrderRespectsColumns`](../backend/translate/parity_test.go) | Two synthetic left/right-column figure positions tested; page-top citation order, later-section tables, equation ordering, unlocated floats and concurrent additions remain unported. |
| `tests/translate/test_instructions.py` (6) | Partial | [`TestCapConfirmAndTargetRetention`](../backend/jobs/jobs_test.go)<br>[`TestPartialRetryOnlyFailedAndKeepInstructions`](../backend/jobs/jobs_test.go) | Task instructions preserved through confirm/retry; localized defaults/editor/custom output-contract/read-only prompt differences not tested. |
| `tests/translate/test_langs.py` (13) | Partial | [`TestPagesCheckpointRetryAndFill`](../backend/translate/translate_test.go)<br>[`TestCapConfirmAndTargetRetention`](../backend/jobs/jobs_test.go) | Target metadata survives Japanese fixture/confirmation; actual multilingual prompt swaps, CJK terms, default legacy language and invalid-target settings not established. |
| `tests/translate/test_math_delimiters.py` (7) | Partial | [`TestRepairRetainsAllCellsAndFields`](../backend/translate/parity_test.go)<br>[`TestCodeAliasesAndMathMarkers`](../backend/translate/parity_test.go)<br>[`TestCLITeXRenderingAndEscapedMath`](../backend/cmd/easyread/main_test.go) | One missing-delimiter repair and content-retention guard plus CLI formula inspection tested; translation repair does not exercise KaTeX, every rendered field and repair-no-improvement/fill matrix remain unported. |
| `tests/translate/test_page_check.py` (6) | Partial | [`TestRepairDoneLeavesCrossPageContinuation`](../backend/store/store_test.go)<br>[`TestConcurrentLanesAndMissingPageRetry`](../backend/translate/parity_test.go) | RepairDone and retry of just one omitted batch page tested; blank/reference exemptions and repeatedly missing page final state not fully ported. |
| `tests/translate/test_readmode.py` (4) | Partial | [`TestPagesCheckpointRetryAndFill`](../backend/translate/translate_test.go)<br>[`TestMissingFillRemainsRetryable`](../backend/translate/translate_test.go) | Existing English-only IDs filled in place, missing key remains retryable; initial read-only marking and mixed batch separation not fully tested. |
| `tests/translate/test_repair_guard.py` (1) | Partial | [`TestRepairRetainsAllCellsAndFields`](../backend/translate/parity_test.go)<br>[`TestMissingFillRemainsRetryable`](../backend/translate/translate_test.go) | Explicit removed table cell/field refusal now tested; not claiming equivalence to every field/type shape in the Python fixture. |
| `tests/translate/test_segments.py` (20) | Partial | [`TestMigration`](../backend/config/config_test.go)<br>[`TestSegmentsAndBatches`](../backend/translate/parity_test.go)<br>[`TestConcurrentLanesAndMissingPageRetry`](../backend/translate/parity_test.go)<br>[`TestTermsRespectOtherMeaningsAndCode`](../backend/translate/parity_test.go) | Worker caps/batch splits, concurrent lanes and basic code-safe term unification now tested; seam balancing/order, next-page peeks, paragraph adjacency and reference-block matrix remain partial. |
| `tests/translate/test_sentences.py` (19) | Partial | [`TestJSONNormalizeAndSentences`](../backend/translate/translate_test.go)<br>[`TestFillSentenceAlignment`](../backend/translate/parity_test.go)<br>[`TestCodeAliasesAndMathMarkers`](../backend/translate/parity_test.go)<br>[`TestTranslationAlignmentSurvivesNormalizeThenMerge`](../backend/translate/parity_test.go) | Alignment now also survives full normalize/merge. Korean/Latin spacing, astral sentence offsets, abbreviation split and complex term-replacement matrix remain partial. |
| `tests/translate/test_table_shape.py` (4) | Partial | [`TestJSONNormalizeAndSentences`](../backend/translate/translate_test.go) | Normalizer rejects one flat header; merge/fill/page-transaction no-mark-done scenarios not tested. |
| `tests/translate/test_translate.py` (7) | Partial | [`TestScope`](../backend/translate/translate_test.go)<br>[`TestPagesCheckpointRetryAndFill`](../backend/translate/translate_test.go)<br>[`TestCancellationDoesNotPublish`](../backend/translate/translate_test.go)<br>[`TestChecksReplaceTranslatorButRetainUserNotes`](../backend/translate/parity_test.go) | Scope/range, failed-batch retry, no-publish cancellation and translator-check replacement preserving user notes tested; quota-stop and fence-inside-string cases still absent. |
| `tests/zotero/test_reader.py` (6) | Partial | [`TestZoteroReadOnly`](../backend/importer/importer_test.go) | Read-only SQLite fixture metadata/date/collection path/deleted-item filtering; authors/tags/group libraries, locked DB backup, invalid folder and preferences escape cases not fully tested. |

## HTTP scenarios actually exercised

The eight tests in `backend/server/server_test.go` are routed HTTP evidence. Separate `engines/http_test.go` now covers the original outbound TLS tests. Routed selection and presence suites were added in this regression round.

- `TestHTTPWireContracts`: library token/items, paper state, reader write/part/version agreement; GET smoke checks for version/config/preferences/jobs/trash/library location/import status/chat/update. Merely returning an object is weaker than validating every field.
- `TestHTTPTokenOriginAndInvalidBody`: missing mutation token, cross-origin POST, array/null/multiple/truncated JSON rejection.
- `TestHTTPCorruptReaderPreserved`: HTTP edit fails without changing corrupt reader bytes.
- `TestHTTPSettingsDoNotLeakOrMoveLibrary`: masked key response; config endpoint cannot move library or save task-only instructions; boolean page cap rejected.
- `TestHTTPTraversalAndEmbeddedAssets`: real embedded frontend assets and a paper-asset symlink escape rejection.
- `TestHTTPRestartReadOnlyGate`: mutation gets 409 after restart-required; reading state does not mark paper read.
- `TestHTTPImportAndPrepareRealPDF`: raw PDF upload queues preparation and eventually records one page.
- `TestRejectDNSRebindingHost`: attacker Host rejected before exposing the mutation token.

Not yet demonstrated by these HTTP tests: full `/api/engines` nested shape, all redirect/version combinations, per-paper export/delete/migration concurrency, routed chat attachment follow-up, every validation/status/error body, and each response-write failure. Chat package tests invoke its HTTP handler directly; they do not traverse `server.App` token/routing gates.

## Newly closed original scenarios

The eight previously unmapped files contain 34 original methods. All now have explicit counterparts below. Queue recovery and presence add another 15 original methods. These are assertion mappings with the limits above, not proof that all 620 historical methods are equivalent.

### `tests/app/test_release_i18n.py`

- `test_english_dictionary_has_no_duplicate_keys` → [`TestReleaseEnglishDictionaryHasNoDuplicateKeys`](../backend/config/release_i18n_test.go).
- `test_new_feature_translations_preserve_named_parameters` → [`TestReleaseFeatureTranslationsPreserveNamedParameters`](../backend/config/release_i18n_test.go).

### `tests/chat/test_chat_lean.py`

- `test_read_search_web_tools_and_user_choices_kept` → [`TestClaudeChatReadOnlyToolsAndChoices`](../backend/chat/lean_parity_test.go).
- `test_unknown_option_falls_back_before_any_output` → [`TestClaudeUnsupportedToolsFallbackOnlyBeforeOutput`](../backend/engines/lean_test.go).
- `test_other_errors_are_not_retried` → [`TestClaudeUnsupportedToolsFallbackOnlyBeforeOutput`](../backend/engines/lean_test.go).
- `test_cancel_event_untouched_after_answer` → [`TestClaudeChatLeavesCallerContextActive`](../backend/chat/lean_parity_test.go).
- `test_codex_chat_answer_and_retranslate_use_lean_cfg` → [`TestCodexChatAnswerRetranslateLeanIsolation`](../backend/chat/lean_parity_test.go).

### `tests/engines/test_netcheck.py`

- `test_reachable_when_any_http_response` → [`TestNetworkAnyHTTPResponseAndCache`](../backend/engines/netcheck_test.go).
- `test_unreachable_local_says_open_it` → [`TestNetworkUnreachableLocal`](../backend/engines/netcheck_test.go).
- `test_overseas_preset_mentions_vpn` → [`TestNetworkOverseasPreset`](../backend/engines/netcheck_test.go).
- `test_explain_adds_hint_only_for_network_errors` → [`TestNetworkExplainOnlyNetworkErrors`](../backend/engines/netcheck_test.go).
- `test_certificate_failure_is_not_reported_as_vpn_problem` → [`TestNetworkCertificateNoVPNOrRetry`](../backend/engines/netcheck_test.go).

### `tests/library/test_fs_retry.py`

- `test_transient_invalid_argument_recovers` → [`TestRetryWriteTransientInvalidArgumentRecovers`](../backend/store/fs_retry_test.go).
- `test_persistent_error_is_reported_after_bounded_retries` → [`TestRetryWritePersistentErrorIsBounded`](../backend/store/fs_retry_test.go).
- `test_existing_lock_is_never_bypassed` → [`TestExistingLockNeverBypassed`](../backend/store/fs_retry_test.go).
- `test_lock_write_failure_closes_descriptor_and_removes_only_owned_lock` → [`TestLockWriteFailureClosesDescriptorAndRemovesOwnedLock`](../backend/store/fs_retry_test.go).

### `tests/pdf/test_pdf_concurrency.py`

- `test_render_extract_crop_and_model_images_cannot_overlap` → [`TestNativeRenderExtractCropAndModelImagesCannotOverlap`](../backend/pdf/concurrency_test.go).
- `test_pdf_handles_close_after_save_error` → [`TestNativeProcessesCloseAfterModelImageSaveError`](../backend/pdf/concurrency_test.go).

### `tests/server/test_http.py`

- `test_untrusted_certificate_is_rejected` → [`TestOutboundTLSUntrustedCertificateRejected`](../backend/engines/http_test.go).
- `test_explicit_trusted_certificate_is_used` → [`TestOutboundTLSExplicitTrustedCertificateUsed`](../backend/engines/http_test.go).
- `test_hostname_mismatch_is_rejected` → [`TestOutboundTLSHostnameMismatchRejected`](../backend/engines/http_test.go).

### `tests/server/test_parallel_papers.py`

- `test_config_is_bounded_and_starts_the_requested_workers` → [`TestPaperWorkerCountBoundsAndStartsRequestedWorkers`](../backend/jobs/parallel_papers_test.go).
- `test_paper_concurrency_persists_in_existing_config` → [`TestPaperConcurrencyPersistsInExistingConfig`](../backend/jobs/parallel_papers_test.go).
- `test_settings_reject_out_of_range_or_non_integer_paper_count` → [`TestSettingsRejectOutOfRangeOrNonIntegerPaperCount`](../backend/jobs/parallel_settings_test.go).
- `test_settings_save_allowed_paper_counts` → [`TestSettingsSaveAllowedPaperCounts`](../backend/jobs/parallel_settings_test.go).
- `test_two_papers_run_together_and_third_waits` → [`TestTwoPapersRunTogetherAndThirdWaits`](../backend/jobs/parallel_papers_test.go).
- `test_finishing_old_task_preserves_requeued_tasks_cancel_event` → [`TestFinishingOldTaskPreservesRequeuedCancelHandle`](../backend/jobs/parallel_papers_test.go).

### `tests/server/test_selection_translate.py`

- `test_translation_preserves_selection_context_and_does_not_write` → [`TestSelectionContextAndReadOnly`](../backend/server/selection_parity_test.go).
- `test_cached_answer_includes_phonetics_without_another_model_call` → [`TestSelectionCachesPhonetics`](../backend/server/selection_parity_test.go).
- `test_context_and_model_configuration_changes_invalidate_cache` → [`TestSelectionInvalidatesContextAndModel`](../backend/server/selection_parity_test.go).
- `test_lru_evicts_least_recently_used_instead_of_most_recently_read` → [`TestSelectionLRUEviction`](../backend/server/selection_parity_test.go).
- `test_invalid_selection_and_cross_site_requests_never_call_model` → [`TestSelectionValidationAndCrossSiteNoModel`](../backend/server/selection_parity_test.go).
- `test_error_and_empty_answer_can_be_retried` → [`TestSelectionProviderFailureAndEmptyRetried`](../backend/server/selection_parity_test.go).
- `test_long_multiline_selection_is_not_silently_truncated` → [`TestSelectionLongMultilineNotTruncated`](../backend/server/selection_parity_test.go).

### `tests/server/test_presence.py`

- `test_waits_longer_before_first_page` → [`TestPresenceWaitsLongerBeforeFirstPage`](../backend/server/presence_test.go).
- `test_grace_after_last_page_closes` → [`TestPresenceGraceAfterLastPageCloses`](../backend/server/presence_test.go).
- `test_busy_blocks_exit` → [`TestPresenceBusyBlocksExit`](../backend/server/presence_test.go).
- `test_exits_after_last_page_closes` → [`TestPresenceServerExitsAfterLastPageCloses`](../backend/server/presence_test.go).
- `test_other_site_rejected` → [`TestPresenceServerRejectsOtherSite`](../backend/server/presence_test.go).
- `test_keep_turns_off_auto_exit` → [`TestPresenceKeepTurnsOffAutoExit`](../backend/server/presence_test.go).

### `tests/server/test_job_queue_recovery.py`

- `test_delete_during_preparation_keeps_existing_and_new_jobs_running` → [`TestDeleteDuringPreparationKeepsExistingAndNewJobsRunning`](../backend/jobs/recovery_edges_test.go).
- `test_delete_during_translation_keeps_queue_running` → [`TestDeleteDuringTranslationAndUsageFlushKeepsQueueRunning`](../backend/jobs/recovery_edges_test.go).
- `test_delete_during_translation_usage_flush_keeps_queue_running` → [`TestDeleteDuringTranslationAndUsageFlushKeepsQueueRunning`](../backend/jobs/recovery_edges_test.go).
- `test_delete_during_running_state_write_keeps_queue_running` → [`TestDeleteDuringRunningStateWriteKeepsQueueRunning`](../backend/jobs/recovery_edges_test.go).
- `test_running_state_write_failure_does_not_kill_worker` → [`TestStatusWriteFailuresDoNotKillWorker`](../backend/jobs/recovery_edges_test.go).
- `test_cancelled_state_write_failure_does_not_kill_worker` → [`TestStatusWriteFailuresDoNotKillWorker`](../backend/jobs/recovery_edges_test.go).
- `test_error_state_write_failure_does_not_kill_worker` → [`TestStatusWriteFailuresDoNotKillWorker`](../backend/jobs/recovery_edges_test.go).
- `test_missing_file_error_in_existing_directory_is_not_silenced` → [`TestExistingWorkspaceWriteErrorsAreNotSilenced`](../backend/jobs/recovery_edges_test.go).
- `test_permission_error_is_not_silenced` → [`TestExistingWorkspaceWriteErrorsAreNotSilenced`](../backend/jobs/recovery_edges_test.go).

## New Go test inventory

### `backend/chat/chat_test.go` (7)

- `TestLegacyThreadsAndMutation`
- `TestImageValidationAndPaths`
- `TestNDJSONStoresAnswerAndImages`
- `TestFailureCleansAttachments`
- `TestBrokenPipeSavesPartial`
- `TestCLIImagesAndColorFilteredContext`
- `TestInvalidModelSpecificEffortBeforeStreaming`

### `backend/chat/checked_integration_test.go` (2)

- `TestCheckedUserPathsBlockBeforeGeneration`
- `TestCheckedSelectionCanceledBeforePreflight`

### `backend/chat/lean_parity_test.go` (3)

- `TestClaudeChatReadOnlyToolsAndChoices`
- `TestClaudeChatLeavesCallerContextActive`
- `TestCodexChatAnswerRetranslateLeanIsolation`

### `backend/cmd/easyread/main_test.go` (8)

- `TestCLIListStatusDoNotStartQueuedJobs`
- `TestCLIMergeArrayAndDiscussionDedup`
- `TestCLIBlocksDoneWithoutReplace`
- `TestCLICheckReportsRefsAnchorsAndExitStatus`
- `TestCLIMigrateCopiesUnknownAndPreservesExisting`
- `TestCLIServeReusesExistingWithoutStartingJobs`
- `TestCLIReplaceOnlySelectedPages`
- `TestCLITeXRenderingAndEscapedMath`

### `backend/config/config_test.go` (4)

- `TestSaveMasksAndScopesKeys`
- `TestTemporaryLibraryNotSavedAndPrefsIsolated`
- `TestCorruptConfigCannotBeOverwritten`
- `TestMigration`

### `backend/config/release_i18n_test.go` (2)

- `TestReleaseEnglishDictionaryHasNoDuplicateKeys`
- `TestReleaseFeatureTranslationsPreserveNamedParameters`

### `backend/engines/checked_test.go` (5)

- `TestRunCheckedProbesBeforeOneGeneration`
- `TestRunCheckedExplainsFailureWithoutRetry`
- `TestRunCheckedCancellationIdentity`
- `TestStreamCheckedProbeAndNoReplayAfterPartialOutput`
- `TestStreamCheckedCallbackAndCancellationIdentity`

### `backend/engines/engines_test.go` (13)

- `TestChatAndResponses`
- `TestStreaming`
- `TestKeyIsolation`
- `TestStreamIncomplete`
- `TestErrorRedaction`
- `TestCLI`
- `TestCLICancelProcessGroup`
- `TestAPICancellation`
- `TestResponsesIncomplete`
- `TestEngineConfigDoesNotShareKey`
- `TestEveryCLIAdapter`
- `TestSafeExtraArguments`
- `TestCLIErrorRedaction`

### `backend/engines/http_test.go` (4)

- `TestOutboundTLSUntrustedCertificateRejected`
- `TestOutboundTLSExplicitTrustedCertificateUsed`
- `TestOutboundTLSHostnameMismatchRejected`
- `TestOutboundTLSDirectoryAndInvalidExplicitFile`

### `backend/engines/image_test.go` (12)

- `TestImageEditMultipartActualImage`
- `TestImageSignedDownloadNoCredentials`
- `TestImageEditRejectsInvalidOutputs`
- `TestImageHTTPFailureNoRetryOrLeak`
- `TestImageCancellationDiscardsLateResponse`
- `TestImagePreCancelledAndInvalidSourceNoRequest`
- `TestCodexImageSubscriptionAuthAndCleanup`
- `TestCodexImageNoToolResultNoRetry`
- `TestCodexFreshGeneratedArtifactImported`
- `TestCodexArtifactScopeFreshnessAndSymlinks`
- `TestCodexImageInvalidOrSymlinkResultRejected`
- `TestCodexImageCancellationTimeoutAndPreCancelled`

### `backend/engines/lean_test.go` (2)

- `TestClaudeUnsupportedToolsFallbackOnlyBeforeOutput`
- `TestCodexLeanPreservesConfigDisablesMCPAndFeatures`

### `backend/engines/netcheck_test.go` (7)

- `TestNetworkAnyHTTPResponseAndCache`
- `TestNetworkUnreachableLocal`
- `TestNetworkOverseasPreset`
- `TestNetworkExplainOnlyNetworkErrors`
- `TestNetworkCertificateNoVPNOrRetry`
- `TestNetworkRegionCancellationAndCredentials`
- `TestNetworkCodexHomeProvider`

### `backend/engines/stream_parity_test.go` (6)

- `TestThinkTagsEveryChunkBoundary`
- `TestUnclosedThinkHiddenPlainAnglesRemain`
- `TestSSEFramingAndInvalidEvents`
- `TestSSETerminalFailuresNoRetry`
- `TestExplicitStreamRejectionFallsBackOnce`
- `TestAuthFailureNeverUsesStreamFallback`

### `backend/exporter/export_test.go` (4)

- `TestMarkdownBundlesImagesAndRejectsTraversal`
- `TestHTMLInlinesAssetsAndEscapesData`
- `TestRealReaderOfflineAndMobile`
- `TestLocalizedRealReader`

### `backend/importer/importer_test.go` (5)

- `TestFetchCitation`
- `TestFolderDeduplication`
- `TestZoteroReadOnly`
- `TestLinkedImportDoesNotPrepare`
- `TestConcurrentStartClose`

### `backend/jobs/jobs_test.go` (6)

- `TestCapConfirmAndTargetRetention`
- `TestPartialRetryOnlyFailedAndKeepInstructions`
- `TestCancelAndIndependentSmallQueue`
- `TestRestartResumesTextButNeverInFlightImage`
- `TestUnreadableJobPreserved`
- `TestExplicitPagesRestartSkipsCommittedCheckpoint`

### `backend/jobs/parallel_papers_test.go` (6)

- `TestPaperWorkerCountBoundsAndStartsRequestedWorkers`
- `TestPaperConcurrencyPersistsInExistingConfig`
- `TestTwoPapersRunTogetherAndThirdWaits`
- `TestFinishingOldTaskPreservesRequeuedCancelHandle`
- `TestOldRunFailureDoesNotOverwriteRequeuedRun`
- `TestCancellationFreesOneSlotWithoutCancellingOtherPapers`

### `backend/jobs/parallel_settings_test.go` (2)

- `TestSettingsRejectOutOfRangeOrNonIntegerPaperCount`
- `TestSettingsSaveAllowedPaperCounts`

### `backend/jobs/recovery_edges_test.go` (6)

- `TestDeleteDuringPreparationKeepsExistingAndNewJobsRunning`
- `TestDeleteDuringTranslationAndUsageFlushKeepsQueueRunning`
- `TestDeleteDuringRunningStateWriteKeepsQueueRunning`
- `TestStatusWriteFailuresDoNotKillWorker`
- `TestExistingWorkspaceWriteErrorsAreNotSilenced`
- `TestRunnerPanicDoesNotKillWorker`

### `backend/pdf/concurrency_test.go` (6)

- `TestNativeRenderExtractCropAndModelImagesCannotOverlap`
- `TestNativeProcessesCloseAfterModelImageSaveError`
- `TestNativeRenderMemoryFailureRetriesFreshProcessAtHalfScale`
- `TestInvalidRenderScaleDoesNotStartNativeProcess`
- `TestNativeGateIsHeldUntilChildCleanupCompletes`
- `TestExtremeFiniteRenderDimensionsStayBounded`

### `backend/pdf/pdf_test.go` (16)

- `TestPrepareCropAndSelection`
- `TestRotatedCropBox`
- `TestUnicodeRowsAndNormalization`
- `TestRenderLimits`
- `TestManualCropResetAndLayout`
- `TestCancellation`
- `TestAnnotationLinks`
- `TestRotationTextCoordinates`
- `TestCrossPageLocations`
- `TestColumnRegionsKeepGap`
- `TestSymlinkVariantEscape`
- `TestRotatedGlyphNormalization`
- `TestAutomaticFigureAndConservativeBlankPages`
- `TestDerivedDirectorySymlinkIsRejected`
- `TestMetadataTitleDoesNotOverrideUserTitle`
- `TestSelectableCacheReuseAndInvalidation`

### `backend/server/folder_picker_test.go` (8)

- `TestFolderPickChosenPathAndEnvironment`
- `TestFolderPickCancelReturnsEmpty`
- `TestFolderPickWindowsFailureFallsBack`
- `TestFolderPickUnavailable`
- `TestFolderPickPowerShellASCIIAndExplorerStyle`
- `TestFolderPickLinuxSelectionAndCancel`
- `TestFolderPickMacUsesEnvironmentAndCancellation`
- `TestFolderPickContextAndRootPaths`

### `backend/server/library_location_test.go` (7)

- `TestLocationCopyPreservesSourceTrashAndUnknown`
- `TestLocationMergeSkipsSamePDFAndCollision`
- `TestLocationFailureRollsBackOnCorruptConfig`
- `TestLocationSymlinkRejected`
- `TestLocationBusyAndNestedBlocked`
- `TestIncompleteCleanupConfinedToTarget`
- `TestRemoteAndReadyMarkersRemainUsable`

### `backend/server/pdf_export_test.go` (5)

- `TestPDFBridgeAuthentication`
- `TestPDFBridgeFakeEngine`
- `TestPDFCopyNoOverwrite`
- `TestPDFRestartStatus`
- `TestPDFExportFakeExecutable`

### `backend/server/presence_test.go` (12)

- `TestPresenceWaitsLongerBeforeFirstPage`
- `TestPresenceGraceAfterLastPageCloses`
- `TestPresenceBusyBlocksExit`
- `TestPresenceServerExitsAfterLastPageCloses`
- `TestPresenceServerRejectsOtherSite`
- `TestPresenceKeepTurnsOffAutoExit`
- `TestPresenceQuickConnectionUsesNormalGrace`
- `TestPresencePingPongAndProtocolCleanup`
- `TestPresenceRejectsInvalidHandshake`
- `TestPresenceKeepRequiresToken`
- `TestPresenceBackgroundCancellationClosesHijackedSocket`
- `TestPresenceDefaultGraceDurations`

### `backend/server/selection_parity_test.go` (8)

- `TestSelectionContextAndReadOnly`
- `TestSelectionCachesPhonetics`
- `TestSelectionInvalidatesContextAndModel`
- `TestSelectionLRUEviction`
- `TestSelectionValidationAndCrossSiteNoModel`
- `TestSelectionProviderFailureAndEmptyRetried`
- `TestSelectionLongMultilineNotTruncated`
- `TestSelectionOriginalCaptionsAndListContext`

### `backend/server/server_test.go` (8)

- `TestHTTPWireContracts`
- `TestHTTPTokenOriginAndInvalidBody`
- `TestHTTPCorruptReaderPreserved`
- `TestHTTPSettingsDoNotLeakOrMoveLibrary`
- `TestHTTPTraversalAndEmbeddedAssets`
- `TestHTTPRestartReadOnlyGate`
- `TestHTTPImportAndPrepareRealPDF`
- `TestRejectDNSRebindingHost`

### `backend/server/updates_test.go` (3)

- `TestVersionOrdering`
- `TestUpdateThrottleDisableForceFailure`
- `TestOpenLinksIDHashAndSafeAnchor`

### `backend/store/fs_retry_test.go` (7)

- `TestRetryWriteTransientInvalidArgumentRecovers`
- `TestRetryWritePersistentErrorIsBounded`
- `TestExistingLockNeverBypassed`
- `TestLockWriteFailureClosesDescriptorAndRemovesOwnedLock`
- `TestRetryWriteOnlyRetriesWindowsTransientErrors`
- `TestLockRetriesOwnerWriteWithoutBypassingOwnership`
- `TestOldUnlockPreservesReplacementLock`

### `backend/store/store_test.go` (11)

- `TestTextHashUTF16`
- `TestReaderOpsAndHistory`
- `TestCorruptProtection`
- `TestConcurrentUpdatesPreserveUnknown`
- `TestImportLinksTrashAndRestore`
- `TestListIncludesCorruptEntries`
- `TestConvertPDFToLinkRequiresEqualCopyAndConsent`
- `TestRepairDoneLeavesCrossPageContinuation`
- `TestInvalidOpHasNoPersistedSideEffects`
- `TestReaderDeletionWinsDelayedCreationBothOrders`
- `TestReaderReadingPositionOffsetRatioAndDelayedProgress`

### `backend/translate/figures_test.go` (4)

- `TestImageGenerationReuseAndInputIdentity`
- `TestImageLateResultCannotOverwriteEdits`
- `TestImageCancelAfterResponseDoesNotPublish`
- `TestImageFailureDoesNotFallbackToPaidAPI`

### `backend/translate/parity_test.go` (12)

- `TestSegmentsAndBatches`
- `TestConcurrentLanesAndMissingPageRetry`
- `TestRepairRetainsAllCellsAndFields`
- `TestCodeAliasesAndMathMarkers`
- `TestFillSentenceAlignment`
- `TestContinuationRequiresVerifiedSourceAndExactSuffix`
- `TestTermsRespectOtherMeaningsAndCode`
- `TestMergeDoneIndependentOfReplace`
- `TestChecksReplaceTranslatorButRetainUserNotes`
- `TestConsistencyCannotOverwriteReaderEdits`
- `TestFloatOrderRespectsColumns`
- `TestTranslationAlignmentSurvivesNormalizeThenMerge`

### `backend/translate/translate_test.go` (6)

- `TestScope`
- `TestJSONNormalizeAndSentences`
- `TestMergePreservesConcurrentChangesAndReader`
- `TestPagesCheckpointRetryAndFill`
- `TestCancellationDoesNotPublish`
- `TestMissingFillRemainsRetryable`

## Original method inventory

All original methods below are counted once. The file mapping above applies conservatively; any individual method not identified as a directly asserted scenario remains unverified, not implicitly covered.

### `tests/app/test_api_keys.py` (4)

- `test_image_service_keys_are_masked_preserved_and_isolated_by_endpoint`
- `test_invalid_image_options_do_not_save`
- `test_switch_custom_endpoints_and_reload`
- `test_legacy_key_only_migrates_to_current_endpoint`

### `tests/app/test_folder_pick.py` (5)

- `test_returns_chosen_path_without_trailing_separator`
- `test_cancel_returns_empty`
- `test_windows_dialog_failure_falls_back_to_typing`
- `test_unavailable_dialog_returns_none`
- `test_powershell_script_is_ascii_and_uses_the_explorer_style_dialog`

### `tests/app/test_i18n.py` (7)

- `test_system_lang_from_electron`
- `test_system_lang_from_env`
- `test_choice_overrides_system`
- `test_tr`
- `test_inject`
- `test_every_ui_string_has_english`
- `test_traditional_interface_dictionary_and_injection`

### `tests/app/test_open_link.py` (5)

- `test_opens_paper_by_fingerprint_with_block_anchor`
- `test_opens_paper_by_id`
- `test_unknown_or_malformed_links_fall_back_to_library`
- `test_unsafe_block_is_dropped`
- `test_version_reports_api_level`

### `tests/app/test_release_i18n.py` (2)

- `test_english_dictionary_has_no_duplicate_keys`
- `test_new_feature_translations_preserve_named_parameters`

### `tests/app/test_updates.py` (5)

- `test_compare`
- `test_asks_once_a_day`
- `test_offline_is_quiet`
- `test_forced_check_offline_reports_failure`
- `test_disabled_does_not_ask`

### `tests/bulk/test_folder.py` (3)

- `test_folders_become_categories`
- `test_missing_folder`
- `test_import_same_pdf_twice_gets_both_categories`

### `tests/bulk/test_runner.py` (8)

- `test_scan_counts_without_importing`
- `test_import_keeps_metadata_categories_tags_and_date`
- `test_without_tags_and_second_run_only_adds_categories`
- `test_fetch_downloads_items_with_doi`
- `test_failed_download_is_recorded_and_others_continue`
- `test_linked_import_preserves_original_and_deduplicates_reader_data`
- `test_linked_mode_rejects_network_download_before_starting`
- `test_category_names_are_clipped`

### `tests/chat/test_answer_styles.py` (13)

- `test_prompt_switches_language_without_losing_source_or_history`
- `test_standard_question_keeps_later_sections_with_or_without_selection`
- `test_xray_mode_reconstructs_and_checks_instead_of_summarizing`
- `test_ste_context_only_includes_annotations_when_the_question_requests_them`
- `test_all_modes_and_engines_require_renderable_math_output`
- `test_ste_prompt_preserves_source_conditions_and_requirement_strength`
- `test_long_paper_excerpt_is_bounded_and_keeps_late_results`
- `test_legacy_messages_do_not_inherit_later_ste_badges`
- `test_partially_imported_paper_identifies_missing_pages`
- `test_note_question_reply_keeps_chinese_answer_and_formula`
- `test_stream_and_followup_remember_mode_and_pin_complete_answer`
- `test_every_declared_mode_round_trips_through_the_server`
- `test_invalid_mode_is_http_400_before_any_model_call`

### `tests/chat/test_chat.py` (4)

- `test_openai_stream_strips_think`
- `test_model_list_picks_preset_key`
- `test_threads_and_legacy`
- `test_marks_only_when_asked_and_refs`

### `tests/chat/test_chat_effort.py` (2)

- `test_explicit_override_does_not_change_card_or_translation`
- `test_legacy_requests_and_invalid_levels`

### `tests/chat/test_chat_images.py` (6)

- `test_validated_images_persist_as_scoped_png_and_survive_chat_history`
- `test_invalid_image_type_count_size_and_pixels_are_rejected_before_writes`
- `test_only_four_recent_safe_images_are_forwarded`
- `test_codex_receives_image_paths_through_existing_adapter`
- `test_claude_read_prompt_and_api_stream_include_images`
- `test_http_chat_persists_attachment_serves_it_and_keeps_it_in_followups`

### `tests/chat/test_chat_lean.py` (5)

- `test_read_search_web_tools_and_user_choices_kept`
- `test_unknown_option_falls_back_before_any_output`
- `test_other_errors_are_not_retried`
- `test_cancel_event_untouched_after_answer`
- `test_codex_chat_answer_and_retranslate_use_lean_cfg`

### `tests/chat/test_chat_store.py` (2)

- `test_empty_chat_can_be_created_renamed_and_deleted`
- `test_same_clock_tick_does_not_alias_threads_or_pinned_answers`

### `tests/engines/test_agent_cli.py` (15)

- `test_grok_prompt_goes_through_file_and_reads_text`
- `test_grok_error_object`
- `test_grok_pretty_printed_json`
- `test_agy_sends_one_user_event_on_stdin`
- `test_agy_unsupported_effort_not_passed`
- `test_agy_error_status`
- `test_cursor_prompt_on_stdin_read_only`
- `test_cursor_error`
- `test_empty_output_is_error`
- `test_path_prefers_configured_command_then_defaults`
- `test_grok_agent_not_mistaken_for_cursor`
- `test_engines_dispatch_and_image_mode`
- `test_chat_model_card_kept_and_mapped`
- `test_chat_effort_limits`
- `test_cursor_round_trip`

### `tests/engines/test_api_stream_errors.py` (13)

- `test_think_tags_at_every_chunk_boundary`
- `test_unclosed_think_is_hidden_but_plain_angles_survive`
- `test_chat_terminal_errors_and_unexpected_eof`
- `test_responses_terminal_errors_and_unexpected_eof`
- `test_valid_completion_markers`
- `test_multiline_sse_data_and_comments`
- `test_relay_framing_without_blank_lines`
- `test_invalid_sse_does_not_finish_successfully`
- `test_complete_does_not_return_truncated_or_broken_stream`
- `test_already_cancelled_stream_does_not_call_api`
- `test_retry_after_date_invalid_and_seconds`
- `test_rate_limit_backs_off_past_a_minute`
- `test_failed_response_reports_error_without_output`

### `tests/engines/test_chat_tools.py` (4)

- `test_opencode_research_permissions_preserve_default_deny`
- `test_codebuddy_and_zcode_research_do_not_enable_writes`
- `test_grok_search_is_chat_only`
- `test_codex_enables_live_search_without_changing_translation_or_sandbox`

### `tests/engines/test_codebuddy_cli.py` (6)

- `test_stdin_images_safe_args_and_usage`
- `test_jsonl_and_explicit_region`
- `test_missing_error_and_nonzero_results`
- `test_cancel_and_timeout_forwarded_without_retry`
- `test_extra_args_cannot_enable_tools_or_mcp`
- `test_path_dispatch_and_model_mapping`

### `tests/engines/test_codex_image.py` (9)

- `test_subscription_auth_and_real_image_result`
- `test_no_tool_result_is_an_error_and_is_not_retried`
- `test_generated_artifact_link_is_imported_without_a_copy`
- `test_artifact_links_cannot_import_old_or_unrelated_files`
- `test_generated_images_allow_system_directory_aliases_but_not_escaped_paths`
- `test_no_proxy_override_still_removes_paid_credentials`
- `test_cancel_or_timeout_reaps_process_and_cleans_workspace`
- `test_cancelled_call_never_starts_cli`
- `test_invalid_image_and_symlink_are_not_published`

### `tests/engines/test_codex_translation_options.py` (7)

- `test_cli_overrides_and_legacy_defaults`
- `test_translation_options_do_not_change_chat_model`
- `test_cache_and_persistence`
- `test_settings_ui`
- `test_claude_explicit_effort`
- `test_card_effort_survives_save_and_chat_selection`
- `test_api_effort_request_formats`

### `tests/engines/test_engine_lean.py` (11)

- `test_server_names_skip_subtables`
- `test_args_disable_each_server_and_notify_keep_provider`
- `test_missing_or_odd_config_only_turns_features_off`
- `test_codex_home_env`
- `test_for_translation_marks_cli_engines_only`
- `test_claude_lean_uses_tools_read_and_keeps_model`
- `test_claude_falls_back_when_option_unknown`
- `test_claude_other_errors_not_retried`
- `test_codex_lean_passes_overrides`
- `test_codex_falls_back_on_config_error`
- `test_codex_other_errors_not_retried`

### `tests/engines/test_image_edit.py` (5)

- `test_uploads_actual_image_and_prompt_and_decodes_response`
- `test_url_result_download_does_not_forward_key`
- `test_invalid_outputs_are_not_accepted_as_images`
- `test_http_failure_is_not_retried_or_leaked`
- `test_cancel_discards_late_response`

### `tests/engines/test_netcheck.py` (5)

- `test_reachable_when_any_http_response`
- `test_unreachable_local_says_open_it`
- `test_overseas_preset_mentions_vpn`
- `test_explain_adds_hint_only_for_network_errors`
- `test_certificate_failure_is_not_reported_as_vpn_problem`

### `tests/engines/test_openai_api.py` (7)

- `test_responses_complete`
- `test_chat_complete_streams_and_counts_usage_once`
- `test_complete_reads_whole_json_when_stream_is_ignored`
- `test_responses_stream`
- `test_cancel_does_not_wait_for_response`
- `test_chat_drops_temperature_when_rejected`
- `test_models`

### `tests/engines/test_opencode_runtime.py` (3)

- `test_prompt_is_sent_directly_without_registering_or_selecting_an_agent`
- `test_failed_call_waits_for_process_exit_before_removing_workdir`
- `test_runtime_does_not_disable_provider_config_or_user_login`

### `tests/engines/test_parse_json.py` (10)

- `test_existing_top_level_block_array_is_preserved`
- `test_unclosed_outer_object_does_not_return_its_block_array`
- `test_code_fences_and_brackets_in_json_strings_are_preserved`
- `test_empty_object_and_array_are_valid`
- `test_citation_brackets_before_object`
- `test_brace_in_prose_before_object`
- `test_broken_outer_object_does_not_return_inner`
- `test_tex_backslash_repair`
- `test_raw_newline_inside_string`
- `test_refusal_prose_has_no_json`

### `tests/engines/test_qoder_cli.py` (5)

- `test_detects_cn_commands_without_switching_to_international_login`
- `test_chat_can_research_but_translation_remains_read_only`
- `test_json_and_stream_output`
- `test_cli_failure_is_not_returned_as_answer`
- `test_model_registration_and_chat_do_not_mutate_saved_config`

### `tests/engines/test_stream_recovery.py` (5)

- `test_explicit_rejection_falls_back_once_for_both_apis`
- `test_disconnect_discards_partial_and_retries_once`
- `test_persistent_disconnect_is_bounded`
- `test_auth_and_output_limit_are_not_retried`
- `test_cancel_during_backoff_does_not_start_retry`

### `tests/engines/test_usage.py` (5)

- `test_claude_subscription_reports_limits_not_cost`
- `test_claude_api_key_reports_cost`
- `test_codex`
- `test_openai_chat_and_deepseek_and_responses`
- `test_meter_and_merge`

### `tests/engines/test_zcode_cli.py` (6)

- `test_long_unicode_prompt_images_login_and_usage`
- `test_json_and_result_event`
- `test_failures_never_become_answers`
- `test_timeout_and_cancel_cleanup_without_retry`
- `test_no_unsupported_overrides`
- `test_detection_translation_and_chat`

### `tests/export/test_mobile_export.py` (3)

- `test_mobile_drops_translated_pages_keeps_notes`
- `test_full_export_unchanged`
- `test_exports_translated_image_with_original_and_text_fallback`

### `tests/library/test_cloudlib.py` (27)

- `test_missing_drive_returns_in_a_second_and_reopens_gate`
- `test_copy_preserves_all_papers_notes_trash_and_old_library`
- `test_failed_copy_does_not_change_configuration_or_existing_files`
- `test_failed_second_publication_rolls_back_new_papers`
- `test_failed_merge_keeps_existing_target_and_rolls_back_additions`
- `test_locked_rollback_leaves_marker_and_blocks_partial_library_use`
- `test_staging_cleanup_error_reports_success_and_keeps_old_library_read_only`
- `test_marker_cleanup_error_reports_success_and_new_library_can_open`
- `test_configuration_failure_rolls_back_before_reusing_target`
- `test_locked_rollback_marker_preserves_complete_ready_target`
- `test_mismatch_is_not_accepted`
- `test_merge_full_pdf_sha_and_id_collision_keep_target`
- `test_use_does_not_copy_or_remove_source`
- `test_nested_paths_and_temporary_library_rejected`
- `test_unknown_target_with_same_id_never_overwritten`
- `test_busy_job_and_dequeued_job_transition_rejected`
- `test_requests_warm_and_other_pages_block_migration`
- `test_gate_freezes_writes_during_copy_and_until_restart`
- `test_links_and_junctions_cannot_escape_source`
- `test_detection_uses_existing_onedrive_and_subfolder`
- `test_marker_recent_other_host_and_owned_cleanup`
- `test_get_cannot_invoke_mutating_library_routes`
- `test_unwritable_folder_returns_immediately`
- `test_cleanup_restores_target_and_unblocks`
- `test_cleanup_refuses_current_library`
- `test_marker_from_another_computer_does_not_block`
- `test_ready_marker_opens_from_another_path`

### `tests/library/test_cloudlib_detect.py` (7)

- `test_windows_icloud_accepts_both_directory_spellings`
- `test_custom_empty_and_unrelated_folder_get_easyread_child`
- `test_existing_library_and_explicit_easyread_folder_stay_as_selected`
- `test_partial_library_marker_is_not_hidden_in_an_extra_child`
- `test_empty_current_library_location_uses_exact_path`
- `test_normalizing_parent_or_current_path_cannot_bypass_nesting_guard`
- `test_existing_file_is_not_treated_as_a_folder`

### `tests/library/test_fs_retry.py` (4)

- `test_transient_invalid_argument_recovers`
- `test_persistent_error_is_reported_after_bounded_retries`
- `test_existing_lock_is_never_bypassed`
- `test_lock_write_failure_closes_descriptor_and_removes_only_owned_lock`

### `tests/library/test_linked_pdf.py` (10)

- `test_link_import_uses_external_pdf_without_creating_source_copy`
- `test_default_copy_and_duplicate_link_import_preserve_reading_data`
- `test_explicit_conversion_removes_only_equal_stored_copy`
- `test_conversion_rejects_changed_stored_copy_and_internal_target`
- `test_render_extract_engine_image_and_figure_use_link`
- `test_first_reader_open_prepares_linked_pdf_without_eager_import_cache`
- `test_trash_restore_purge_and_cloud_copy_do_not_touch_external_pdf`
- `test_missing_link_is_actionable_and_non_pdf_and_url_are_rejected`
- `test_source_http_serves_external_pdf_and_keeps_other_paths_scoped`
- `test_model_metadata_cannot_change_pdf_source`

### `tests/library/test_reader_ops.py` (2)

- `test_deletion_wins_over_delayed_creation_in_both_orders`
- `test_reading_position_keeps_block_offset_and_ratio`

### `tests/library/test_reader_preparation.py` (10)

- `test_cancelled_read_does_not_send_a_second_error_response`
- `test_open_is_nonblocking_deduplicated_and_crops_follow_layout`
- `test_different_papers_do_not_prepare_pdfs_concurrently`
- `test_queued_translation_is_skipped`
- `test_translation_started_while_waiting_is_skipped`
- `test_worker_exception_releases_gate_and_allows_retry`
- `test_failed_thread_start_releases_registration_and_gate`
- `test_failed_warm_thread_start_also_releases_gate`
- `test_preparation_start_failure_keeps_persistent_http_response_valid`
- `test_api_sends_saved_state_before_pdf_upgrade_finishes`

### `tests/library/test_trash.py` (6)

- `test_list_newest_first`
- `test_restore_and_conflict`
- `test_purge_needs_a_valid_name`
- `test_empty_removes_read_only_files`
- `test_retries_while_sync_holds_files`
- `test_gives_up_after_retries`

### `tests/library/test_unreadable_data.py` (4)

- `test_startup_skips_damaged_job_and_resumes_healthy_job`
- `test_damaged_paper_does_not_hide_healthy_papers_or_change_files`
- `test_unreadable_notes_cannot_be_overwritten_by_an_edit`
- `test_damaged_trash_entry_remains_counted_and_can_be_restored`

### `tests/pdf/test_blank_footnotes.py` (2)

- `test_only_empty_page_skipped_without_model_call`
- `test_existing_content_and_missing_source_are_not_skipped`

### `tests/pdf/test_figure_geometry.py` (31)

- `test_side_caption_keeps_left_and_lower_panels_despite_overestimated_paragraph`
- `test_two_figures_on_same_page_stay_separate`
- `test_separate_subpanel_images_are_kept_without_publisher_logo`
- `test_caption_above_drawing`
- `test_distant_top_row_is_connected_through_lower_panels`
- `test_vector_plot_keeps_label_and_ignores_caption_background_and_footer`
- `test_full_page_scan_and_inline_formula_use_existing_fallback`
- `test_explicit_manual_box_and_table_are_preserved`
- `test_manual_figures_do_not_open_pdf_layout_or_preflight`
- `test_tiled_nested_long_form_falls_back_without_pdfplumber`
- `test_scaled_long_form_that_fits_page_is_safe`
- `test_safe_page_geometry_still_runs_beside_tiled_page`
- `test_page_layout_is_closed_even_when_geometry_fails`
- `test_nearby_table_graphic_is_not_assigned_to_figure`
- `test_white_background_rect_does_not_pull_body_text_below_caption`
- `test_white_background_rect_does_not_reach_header`
- `test_clipped_drawing_is_ignored`
- `test_image_white_margin_does_not_reach_header`
- `test_header_above_rule_is_not_a_label`
- `test_separated_panels_join_unless_body_text_between`
- `test_invisible_text_is_not_a_label`
- `test_labels_below_caption_do_not_extend_box`
- `test_crop_leaves_out_caption_below`
- `test_crop_stops_at_caption_drawn_over_image_bottom`
- `test_tight_model_box_grows_to_the_drawing`
- `test_model_box_picks_the_drawing_the_caption_is_not_nearest_to`
- `test_model_box_is_used_when_no_drawing_is_found`
- `test_model_box_figure_is_still_located_by_caption`
- `test_rotated_pdf_uses_rendered_page_coordinates`
- `test_old_layout_refreshes_from_pdf_once_without_changing_translation`
- `test_running_header_and_page_number_are_detected`

### `tests/pdf/test_figures.py` (22)

- `test_corrected_crop_changes_file_and_dimensions`
- `test_reused_id_on_another_page_cannot_share_crop`
- `test_invalid_coordinates_and_pages_do_not_crop`
- `test_failed_crop_drops_stale_source`
- `test_read_then_fill_keeps_image_and_user_edits`
- `test_id_dedup_happens_before_crop`
- `test_retranslated_page_gets_fresh_crop`
- `test_fill_backfills_old_paper_from_layout`
- `test_image_translation_missing_even_when_caption_is_done`
- `test_updated_layout_replaces_stale_auto_crop_once_and_keeps_notes`
- `test_auto_fill_writes_abox_for_physical_width`
- `test_old_crop_missing_abox_is_backfilled_without_recropping`
- `test_custom_image_and_unrecognized_model_box_are_not_replaced`
- `test_graphic_bounds_replace_model_box_crop_without_caption`
- `test_concurrent_retranslation_does_not_get_overwritten`
- `test_failed_refresh_retains_existing_crop`
- `test_deleted_cached_image_is_recreated`
- `test_cleanup_does_not_follow_parent_segments_or_symlinks`
- `test_cleanup_holds_the_write_lock_while_checking_references`
- `test_fill_replaces_orphaned_previous_crop`
- `test_previous_crop_kept_while_another_block_references_it`
- `test_previous_crop_kept_for_manual_crop_snapshot`

### `tests/pdf/test_inline_links.py` (3)

- `test_real_pdf_annotations_survive_extraction_and_prompt_construction`
- `test_old_original_is_backfilled_without_changing_translation_or_notes`
- `test_ambiguous_or_unsafe_links_are_not_guessed`

### `tests/pdf/test_multiregion_layout.py` (6)

- `test_locate_keeps_both_halves_of_a_column_crossing_paragraph`
- `test_single_column_and_three_columns`
- `test_clamping_one_fragment_preserves_the_other_column`
- `test_formula_after_crossing_paragraph_stays_in_its_final_column`
- `test_caption_extends_from_its_column_fragment`
- `test_existing_paper_recalculates_once_without_changing_translation`

### `tests/pdf/test_paragraph_regions.py` (5)

- `test_single_column_figure_labels_do_not_cross_into_neighboring_prose`
- `test_short_axis_labels_and_unrelated_text_are_preserved`
- `test_long_prose_survives_formula_spelling_changes`
- `test_cross_page_continuation_stays_out_of_crop`
- `test_truncated_cross_column_layout_stays_out_of_crop`

### `tests/pdf/test_pdf_concurrency.py` (2)

- `test_render_extract_crop_and_model_images_cannot_overlap`
- `test_pdf_handles_close_after_save_error`

### `tests/pdf/test_pdf_extraction.py` (6)

- `test_shared_long_form_is_not_extracted_on_every_tile`
- `test_crop_origin_and_all_rotations_match_rendered_coordinates`
- `test_unicode_ligature_and_non_bmp_character_keep_their_boxes`
- `test_partial_glyph_is_clamped_and_off_page_glyph_is_skipped`
- `test_extraction_releases_document_after_write_error`
- `test_long_paper_does_not_retain_every_page_stream`

### `tests/pdf/test_pdfwork.py` (11)

- `test_page_variant_bounds_and_safety`
- `test_page_variant_small_image_returns_src`
- `test_page_variant_large_image_resizes`
- `test_warm_variants`
- `test_caption_extends_within_its_column`
- `test_clamp_ignores_other_column`
- `test_formula_gap_stays_in_column`
- `test_single_column_keeps_full_width`
- `test_heading_skips_same_label_inside_figure`
- `test_refresh_layout_recomputes_stale`
- `test_directory_can_be_moved_after_rendering`

### `tests/pdf/test_prepare_worker.py` (4)

- `test_real_child_prepares_pdf_without_touching_user_notes`
- `test_native_page_load_oom_retries_in_new_lower_resolution_process`
- `test_repeated_oom_stops_after_two_children`
- `test_non_memory_errors_are_not_retried`

### `tests/pdf/test_render_limits.py` (8)

- `test_ordinary_paper_keeps_requested_resolution`
- `test_large_and_extreme_aspect_ratios_are_bounded_before_allocation`
- `test_memory_failure_retries_once_at_half_scale`
- `test_native_windows_exception_does_not_reuse_partial_native_state`
- `test_unrelated_error_is_not_hidden_or_retried`
- `test_repeated_memory_failure_stops`
- `test_invalid_dimensions_are_rejected_before_native_render`
- `test_real_oversized_pdf_renders_and_preserves_pdf_coordinates`

### `tests/pdf/test_selectable_text.py` (3)

- `test_spaces_survive_and_cache_is_reused`
- `test_changed_source_invalidates_cache`
- `test_invalid_page_does_not_write_cache`

### `tests/pdf/test_sources.py` (2)

- `test_apostrophes_and_quotes_inside_content_are_kept`
- `test_entities_and_attribute_order`

### `tests/pdf/test_text_regions.py` (3)

- `test_cross_page_tail_does_not_swallow_figure_labels_or_footer`
- `test_cross_column_runs_keep_reading_order_and_leave_gutter_empty`
- `test_unmatched_paragraph_does_not_invent_a_region`

### `tests/server/test_babeldoc_engine.py` (4)

- `test_export_uses_original_filename_and_only_produces_chinese_pdf`
- `test_output_directory_must_exist_before_starting_translation`
- `test_save_failure_preserves_generated_pdf_for_download`
- `test_codex_export_uses_selected_engine_without_requiring_or_copying_api_key`

### `tests/server/test_figure_jobs.py` (8)

- `test_existing_paper_image_only_job_including_legacy_figures_and_user_edits`
- `test_disabled_feature_rejects_new_requests_and_stops_queued_generation`
- `test_whole_paper_skips_current_images_but_single_force_regenerates`
- `test_failed_regeneration_keeps_previous_image_and_reports_failure`
- `test_cancel_stops_images_and_leaves_translation_and_notes_intact`
- `test_invalid_selection_and_duplicate_submission_do_not_queue`
- `test_restart_never_blindly_replays_inflight_generation`
- `test_reader_changes_while_generating_discard_the_old_result`

### `tests/server/test_http.py` (3)

- `test_untrusted_certificate_is_rejected`
- `test_explicit_trusted_certificate_is_used`
- `test_hostname_mismatch_is_rejected`

### `tests/server/test_import_retry.py` (2)

- `test_reimport_after_cli_prepare_failure_retries`
- `test_api_retries_incomplete_entry_but_skips_active_and_prepared`

### `tests/server/test_job_queue_recovery.py` (9)

- `test_delete_during_preparation_keeps_existing_and_new_jobs_running`
- `test_delete_during_translation_keeps_queue_running`
- `test_delete_during_translation_usage_flush_keeps_queue_running`
- `test_delete_during_running_state_write_keeps_queue_running`
- `test_running_state_write_failure_does_not_kill_worker`
- `test_cancelled_state_write_failure_does_not_kill_worker`
- `test_error_state_write_failure_does_not_kill_worker`
- `test_missing_file_error_in_existing_directory_is_not_silenced`
- `test_permission_error_is_not_silenced`

### `tests/server/test_jobs.py` (7)

- `test_duplicate_enqueue_preserves_queued_and_running_task`
- `test_worker_marks_running_before_starting_work`
- `test_cancel_during_preparation_is_not_reported_done`
- `test_cancelled_queue_entry_is_skipped_and_can_be_retried`
- `test_resume_finishes_before_workers_start`
- `test_small_job_ids_are_unique_with_frozen_clock`
- `test_removed_paper_does_not_leave_small_job_queued_forever`

### `tests/server/test_page_cap.py` (21)

- `test_task_instructions_survive_confirmation_and_retry_but_not_new_jobs`
- `test_eighty_pages_wait_without_model_or_usage_and_not_busy`
- `test_confirmed_task_translates_all_eighty_pages`
- `test_explicit_range_eighty_pages_is_not_intercepted`
- `test_zero_cap_is_unlimited`
- `test_forty_pages_are_not_intercepted`
- `test_body_scope_seventy_pages_waits`
- `test_explicit_pages_are_not_intercepted`
- `test_translating_eighty_structured_english_pages_still_requires_confirmation`
- `test_translate_structured_english_only_handles_ten_pages_even_without_cap`
- `test_empty_english_page_plan_does_not_fall_back_to_whole_paper`
- `test_first_pages_choice_only_translates_structured_pages_in_waiting_plan`
- `test_only_remaining_pages_count_toward_cap`
- `test_confirmed_requires_json_true_and_does_not_leak_to_new_tasks`
- `test_read_model_target_and_stored_cap_survive_first_pages_choice`
- `test_confirm_all_preserves_original_scope_read_model_and_target`
- `test_cancel_pending_task_is_done_and_retains_context`
- `test_restart_leaves_confirmation_pending`
- `test_waiting_large_paper_does_not_block_next_small_paper`
- `test_preparation_finishes_before_interception`
- `test_settings_accepts_nonnegative_integers_only`

### `tests/server/test_parallel_papers.py` (6)

- `test_config_is_bounded_and_starts_the_requested_workers`
- `test_paper_concurrency_persists_in_existing_config`
- `test_settings_reject_out_of_range_or_non_integer_paper_count`
- `test_settings_save_allowed_paper_counts`
- `test_two_papers_run_together_and_third_waits`
- `test_finishing_old_task_preserves_requeued_tasks_cancel_event`

### `tests/server/test_pdf_text.py` (3)

- `test_extracted_characters_have_json_content_type`
- `test_selection_requests_preserve_spaces_without_changing_layout_cache`
- `test_other_files_missing_pages_and_unknown_papers_are_not_exposed`

### `tests/server/test_prepare_retry.py` (1)

- `test_failed_import_can_retry_without_invoking_a_model`

### `tests/server/test_presence.py` (6)

- `test_waits_longer_before_first_page`
- `test_grace_after_last_page_closes`
- `test_busy_blocks_exit`
- `test_exits_after_last_page_closes`
- `test_other_site_rejected`
- `test_keep_turns_off_auto_exit`

### `tests/server/test_selection_translate.py` (7)

- `test_translation_preserves_selection_context_and_does_not_write`
- `test_cached_answer_includes_phonetics_without_another_model_call`
- `test_context_and_model_configuration_changes_invalidate_cache`
- `test_lru_evicts_least_recently_used_instead_of_most_recently_read`
- `test_invalid_selection_and_cross_site_requests_never_call_model`
- `test_error_and_empty_answer_can_be_retried`
- `test_long_multiline_selection_is_not_silently_truncated`

### `tests/server/test_stop_jobs.py` (8)

- `test_queued_unprepared_paper_keeps_rendering_without_model`
- `test_queued_prepared_paper_is_cancelled`
- `test_running_job_gets_cancel_signal`
- `test_waiting_for_page_confirmation_becomes_done`
- `test_prepare_only_and_finished_jobs_are_left_alone`
- `test_only_listed_ids_are_stopped`
- `test_error_after_stop_counts_as_cancelled`
- `test_api_rejects_bad_ids`

### `tests/test_issue_preview.py` (6)

- `test_figure_preference_preserves_caption_and_body`
- `test_crop_keeps_manual_choice_and_can_restore`
- `test_crop_rejects_bad_page_before_writing`
- `test_markdown_preserves_answer_and_bundles_only_paper_images`
- `test_opencode_ignores_tool_text_and_surfaces_errors`
- `test_opencode_disables_agent_tools_and_passes_images_as_arguments`

### `tests/translate/test_code_blocks.py` (5)

- `test_model_code_variants_save_both_pages_without_repair`
- `test_direct_merge_preserves_code_and_is_idempotent`
- `test_missing_code_and_unrelated_unknown_types_still_fail`
- `test_fenced_code_is_not_math_or_a_sentence_boundary`
- `test_glossary_does_not_rewrite_code_literals`

### `tests/translate/test_consistency.py` (17)

- `test_reference_has_opening_outline_and_full_previous_page`
- `test_outline_stops_at_references`
- `test_only_first_batch_of_each_later_segment_gets_it`
- `test_prompt_places_reference_before_pages`
- `test_suspects_only_where_term_appears_but_translation_missing`
- `test_swap_only_when_counts_match`
- `test_plain_footnote_is_not_previous_paragraph`
- `test_sentence_alignment_follows_term_swap`
- `test_program_does_the_edit_model_only_names_it`
- `test_majority_rendering_replaces_glossary_when_whole_paper`
- `test_user_edit_during_check_is_not_overwritten`
- `test_no_call_when_nothing_suspicious`
- `test_cancel_during_check_keeps_finished_job`
- `test_merge_time_unify_skips_other_meanings`
- `test_text_engine_seam_owner_gets_whole_next_page`
- `test_resume_batches_only_join_consecutive_pages`
- `test_resume_seam_owner_still_peeks_next_page`

### `tests/translate/test_continuation.py` (26)

- `test_translated_continuation_is_completed_without_duplicate_blocks`
- `test_read_only_continuation_is_completed_and_can_be_translated_in_place`
- `test_uncovered_text_is_retried_and_remains_failed`
- `test_missing_extraction_is_not_accepted`
- `test_untranslated_previous_paragraph_does_not_complete_translation`
- `test_incomplete_previous_page_does_not_complete_continuation`
- `test_matching_text_inside_previous_paragraph_is_not_a_continuation`
- `test_numbers_and_mathematical_operators_must_match`
- `test_pdf_word_breaks_and_ligatures_are_accepted`
- `test_graphics_alongside_covered_text_are_not_silently_dropped`
- `test_missing_or_unreadable_source_pdf_does_not_complete_continuation`
- `test_body_number_matching_page_index_is_not_discarded_as_footer`
- `test_math_after_previous_paragraph_is_not_skipped_to_find_a_match`
- `test_translating_only_untranslated_read_only_continuation_remains_failed`
- `test_continuation_in_fill_batch_waits_for_its_paragraph_translation`
- `test_translating_already_translated_read_only_continuation_needs_no_model_call`
- `test_read_only_formula_page_still_completes_without_translation`
- `test_continuation_can_complete_at_later_page_indices`
- `test_fully_covered_multi_page_batch_completes`
- `test_read_only_multi_page_continuation_can_be_translated_in_one_batch`
- `test_previous_caption_does_not_count_as_paragraph_continuation`
- `test_empty_retranslation_preserves_existing_page_blocks`
- `test_empty_batch_with_an_uncovered_page_remains_failed`
- `test_parallel_seam_waits_for_previous_lane`
- `test_parallel_seam_fails_when_previous_lane_fails`
- `test_parallel_seam_retries_at_the_end_when_not_a_continuation`

### `tests/translate/test_figure_images.py` (12)

- `test_preserves_fallback_and_uses_paper_language_and_reuses_saved_image`
- `test_disabled_unconfigured_and_disabled_figure_text_do_not_call`
- `test_default_uses_subscription_without_api_settings`
- `test_subscription_failure_never_falls_back_to_paid_api`
- `test_failure_or_cancel_keeps_text_and_original`
- `test_late_result_cannot_overwrite_recrop_retranslation_or_new_target`
- `test_missing_asset_can_be_generated_again`
- `test_failure_of_one_figure_does_not_stop_the_next`
- `test_cancel_after_response_does_not_publish_image`
- `test_translation_job_succeeds_when_image_service_fails`
- `test_fill_existing_structure_also_generates_image`
- `test_read_only_mode_does_not_generate`

### `tests/translate/test_float_order.py` (8)

- `test_left_column_figure_precedes_right_column_top_paragraph`
- `test_figure_at_page_top_moves_before_the_paragraph_that_cites_it`
- `test_table_moved_into_a_later_section_comes_back`
- `test_right_column_figure_waits_for_the_left_column`
- `test_equation_under_a_page_top_table_stays_below_it`
- `test_equation_above_a_page_bottom_figure_stays_above_it`
- `test_unlocated_figure_stays_put`
- `test_apply_keeps_blocks_added_after_ordering`

### `tests/translate/test_instructions.py` (6)

- `test_editor_and_translation_share_localized_defaults_including_old_saved_default`
- `test_custom_prompt_is_not_rewritten_when_switching_languages`
- `test_custom_prompt_replaces_style_but_preserves_output_contract`
- `test_editor_keeps_legacy_preferences_and_default_language_adaptation`
- `test_empty_preserves_prompt_and_invalid_settings_never_save`
- `test_translation_fill_and_retranslation_get_preferences_but_read_mode_does_not`

### `tests/translate/test_langs.py` (13)

- `test_chinese_prompt_unchanged`
- `test_every_swap_applies`
- `test_other_language_prompt`
- `test_traditional_chinese_keeps_chinese_rules`
- `test_italian_prompt`
- `test_english_prompt_any_source_language`
- `test_english_target_end_to_end`
- `test_cjk_source_terms_unify`
- `test_old_papers_count_as_chinese`
- `test_reply_language`
- `test_first_translation_records_target`
- `test_import_remembers_target`
- `test_settings_rejects_unknown_target`

### `tests/translate/test_math_delimiters.py` (7)

- `test_bare_tex_is_reported_in_every_rendered_field`
- `test_valid_math_code_and_literal_text_are_not_flagged`
- `test_bare_tex_next_to_valid_math_is_still_reported`
- `test_all_renderer_delimiters_reach_katex_validation`
- `test_translation_and_read_mode_repair_before_saving`
- `test_fill_repairs_missing_delimiters_before_saving`
- `test_fill_keeps_original_when_repair_is_not_better_or_loses_keys`

### `tests/translate/test_page_check.py` (6)

- `test_page_with_text_but_no_blocks_is_missing`
- `test_continuation_or_wrong_page_number_is_not_missing`
- `test_blank_or_reference_pages_are_not_checked`
- `test_unmarks_only_empty_pages_once`
- `test_missing_page_is_translated_again_alone`
- `test_still_missing_is_reported_not_marked_done`

### `tests/translate/test_readmode.py` (4)

- `test_read_marks_en_pages`
- `test_translate_fills_in_place`
- `test_missing_key_retried_only_for_rest`
- `test_mixed_batches_not_combined`

### `tests/translate/test_repair_guard.py` (1)

- `test_missing_block_field_or_table_cell_is_rejected`

### `tests/translate/test_segments.py` (20)

- `test_workers_auto_and_explicit`
- `test_single_lane_keeps_order`
- `test_slowest_lane_not_longer_than_needed`
- `test_fewer_lanes_when_same_speed`
- `test_cut_moves_to_clean_seam`
- `test_footnote_and_leading_table_ignored`
- `test_page_numbers_ignored`
- `test_unify_rewrites_this_batch`
- `test_substring_protected`
- `test_short_terms_left_alone`
- `test_lane_start_told_to_skip_continuation`
- `test_seam_batches_peek_neighbour_image`
- `test_serial_and_range_never_skip`
- `test_conflicting_terms_unified_on_merge`
- `test_only_one_references_block`
- `test_prev_paragraph_skips_footnote_and_flags_unfinished`
- `test_prev_followed_by_math_or_ending_in_math_is_finished`
- `test_fill_prompt_has_no_continuation_rules`
- `test_prev_paragraph_only_from_adjacent_pages`
- `test_old_one_means_auto_until_saved`

### `tests/translate/test_sentences.py` (19)

- `test_para_gets_sents_and_clean_text`
- `test_mismatched_counts_fall_back_to_paragraph`
- `test_empty_sentence_is_not_alignment`
- `test_mark_inside_math_is_kept`
- `test_norm_bars_in_text_are_not_boundaries`
- `test_korean_keeps_space_between_sentences`
- `test_latin_target_keeps_space`
- `test_utf16_offsets_for_astral_chars`
- `test_list_items_and_stray_marks`
- `test_valid_rejects_changed_text`
- `test_split_en_skips_abbreviations_and_math`
- `test_mark_en_round_trip`
- `test_mark_en_follows_stored_sents`
- `test_merge_stores_sents_and_old_format_untouched`
- `test_set_block_text_replaces_or_drops_sents`
- `test_retranslate_prompt_marks_english`
- `test_retranslate_keeps_alignment`
- `test_translate_batch_and_check_quote`
- `test_fill_after_read_aligns_with_program_split`

### `tests/translate/test_table_shape.py` (4)

- `test_flat_head_is_reported_by_checks`
- `test_merge_rejects_flat_head_without_marking_page_done`
- `test_fill_translation_rejects_flat_head`
- `test_translation_page_fails_instead_of_saving_flat_head`

### `tests/translate/test_translate.py` (7)

- `test_failed_batch_is_retried_then_skipped`
- `test_quota_error_stops_remaining_batches`
- `test_cancel_stops`
- `test_scope_body_stops_at_references`
- `test_checks_saved_and_replaced_on_retranslate`
- `test_json_with_code_fence_inside_string`
- `test_range`

### `tests/zotero/test_reader.py` (6)

- `test_metadata_authors_collections_and_tags`
- `test_trash_missing_files_and_standalone_pdfs`
- `test_group_library_collections_go_under_group_name`
- `test_locked_database_falls_back_to_backup`
- `test_not_a_zotero_folder`
- `test_prefs_paths_unescape`

## Reproduce counts

```sh
cd backend
go test -list . ./...
go test -race ./...
```

Count original methods with Python `ast.walk` over `tests/**/test*.py`, selecting `FunctionDef`/`AsyncFunctionDef` names beginning with `test`. Count Go top-level functions with Go test listing or source declarations taking `*testing.T`; exclude `TestMain`. Keep fixture skips and execution failures separate from discovery counts.

## Priority remaining parity work

1. Finish transport Retry-After/backoff and provider-specific argv/env tests. High-risk image and think-tag fixtures are now ported; meter/format differences and native Windows execution remain explicit gaps.
2. Port translation continuation, parallel seams, consistency/glossary, math repair, code blocks, table atomicity, multilingual and sentence-offset regression matrices.
3. Port the automatic PDF figure/paragraph geometry fixtures and remaining native metadata-OOM/cache cases against the new Poppler behavior; do not infer geometry fidelity from one synthetic rectangle.
4. Finish queue stop/page-cap and figure-generation HTTP matrices; concurrency, deletion/write-failure recovery, presence and selection-cache assertions are now directly mapped.
5. Finish migration fault injection, real-platform folder dialogs and remaining update scheduling boundaries. Filesystem retry/lock ownership, TLS certificate and release-dictionary assertions are now directly mapped.

A feature being implemented, a package passing, a JavaScript test passing, or a Go binary building is not evidence that an unported original scenario has been preserved.
