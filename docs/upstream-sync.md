# 手动同步上游

Go/Wails 主分支：`main`，历史开发分支：`feat/wails-desktop`，仓库：`hyskr/easyread`。
上游：`Edwardxlai/easyread`。本项目不配置定时同步。

## 同步步骤

1. 先保存当前改动，确认 `git status` 干净。
2. `git fetch origin main`，其中 origin 指向 Edwardxlai/easyread；fork 指向 hyskr/easyread。
3. `git log --oneline HEAD..origin/main` 查看上游新增提交。
4. 在 Wails 分支执行 `git merge origin/main`，保留 Wails 原生桥接与打包配置；对桌面协议、退出保存、导出和后端启动的冲突逐项处理。
5. 运行 `node --test tests/web/test_*.cjs tests/desktop/test_*.cjs`，以及本次变化涉及的 Python 测试。
6. 重新构建编辑器、Go 后端和 Wails 应用；实际验证导入 PDF、阅读、笔记保存、导出、退出重开。
7. `git push fork main`。需要发布时手动构建并上传安装包，不自动覆盖 Electron 发布。

可在本聊天中发送“同步 EasyRead 上游并更新 Wails”，手动触发上述流程。

## Go 后端分支

Wails 当前构建入口为 `node scripts/build_backend.cjs`，后端位于 `backend/`；前端通过 `easyread/web/assets.go` 嵌入。同步上游时保留这些文件，并将 Python HTTP 接口或存储格式变化对应移植到 Go；不要仅覆盖 Python 代码后认为 Go 后端已同步。验证使用 `cd backend && go test ./...` 和 `EASYREAD_PDF_BIN=/opt/homebrew/bin node tests/backend/native-smoke.cjs`。

## 2026-11-04 同步状态

已合并上游 `19fdc96`，保留 Go 后端与 Wails 桌面入口。Go 已接入引用关系图、引用 URL/PDF 导入和关联校验，参考文献按文本及印刷键合并并保留编号冲突。模型输入通过 Poppler 提取粗体、斜体和内部链接提示，保留抽取原文用于定位。已补参考文献标题有条目但模型未返回条目的检测，以及正文链接定位清理。

PDF 引擎仍有差异：Poppler 内部链接提供页面目标及可见标签，不能完整复现 PDFium 的命名目标、逐字符字体权重及脚注几何识别。模型通过可见标签补齐语义引用键；复杂脚注与数学字体的识别仍需实文验证。

## 2026-11-11 同步状态

合并上游 `3cd21f5`（v1.4.7 发布记录）。前端及保留的 Python 实现同步了批注范围、数学划线、主题恢复、返回位置、文档附件和翻译检查改动。Go 本轮移植 PDF/TXT/Markdown 附件读取、术语有效项过滤、流式正式完成事件校验。合并后 304 项前端/桌面测试与完整 Go 测试通过。

待继续对齐的 Go 行为包括每次 API 请求的细粒度诊断日志、按问题块局部修复、PDF 字体/脚注的几何语义，以及上游新增的用量计时阶段划分。这些差异并未通过本次代码合并自动消除。
