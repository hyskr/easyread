# 手动同步上游

Wails 开发分支：`feat/wails-desktop`，仓库：`hyskr/easyread`。
上游：`Edwardxlai/easyread`。本项目不配置定时同步。

## 同步步骤

1. 先保存当前改动，确认 `git status` 干净。
2. `git fetch origin main`，其中 origin 指向 Edwardxlai/easyread；fork 指向 hyskr/easyread。
3. `git log --oneline HEAD..origin/main` 查看上游新增提交。
4. 在 Wails 分支执行 `git merge origin/main`，保留 Wails 原生桥接与打包配置；对桌面协议、退出保存、导出和后端启动的冲突逐项处理。
5. 运行 `node --test tests/web/test_*.cjs tests/desktop/test_*.cjs`，以及本次变化涉及的 Python 测试。
6. 重新构建编辑器、Go 后端和 Wails 应用；实际验证导入 PDF、阅读、笔记保存、导出、退出重开。
7. `git push fork feat/wails-desktop`。需要发布时手动构建并上传安装包，不自动覆盖 Electron 发布。

可在本聊天中发送“同步 EasyRead 上游并更新 Wails”，手动触发上述流程。

## Go 后端分支

Wails 当前构建入口为 `node scripts/build_backend.cjs`，后端位于 `backend/`；前端通过 `easyread/web/assets.go` 嵌入。同步上游时保留这些文件，并将 Python HTTP 接口或存储格式变化对应移植到 Go；不要仅覆盖 Python 代码后认为 Go 后端已同步。验证使用 `cd backend && go test ./...` 和 `EASYREAD_PDF_BIN=/opt/homebrew/bin node tests/backend/native-smoke.cjs`。
