# EasyRead Wails

macOS Apple Silicon 体验版，使用 Wails 2 与系统 WebKit，复用 EasyRead Python 后端和前端。独立应用名为 EasyRead Wails；默认使用 ~/EasyRead 数据。

## 构建

安装 Go、Xcode Command Line Tools、Node.js 与 Python 后，在仓库根目录执行：

```sh
npm ci
python3 -m venv .venv
.venv/bin/python -m pip install . pyinstaller
./scripts/build_wails.sh
```

输出：desktop-wails/build/bin/EasyRead Wails.app。

## 已知限制

当前为 macOS arm64 体验版。自动更新尚未迁移；Windows/Linux 尚未打包。原生窗口与后端已构建启动，导入、AI 流式输出、导出、深链接及退出保存仍需完整交互回归，暂不作为 Electron 的正式替代版。请避免两个版本同时编辑同一篇论文。

手动同步流程见 [上游同步](../docs/upstream-sync.md)。不包含任何定时任务。
