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

当前为 macOS arm64 体验版。Windows/Linux 尚未打包。macOS 支持窗口尺寸记忆、原生菜单、保存后打开文件、退出保存和后台任务退出拦截。GitHub Actions 在 Wails 分支构建签名包，推送 wails-v* 标签发布。应用启动时检查 Wails 发布，菜单可手动检查；安装更新前按当前固定签名校验包。本地临时签名版不启用自动安装。完整交互仍需继续回归。请避免两个版本同时编辑同一篇论文。

手动同步流程见 [上游同步](../docs/upstream-sync.md)。不包含任何定时任务。

## 来源链接

Wails 导出使用 `easyread-wails://open?...`，Electron 继续使用 `easyread://`。安装 Wails 后重新导出 XMind，即可指向 Wails；旧文件不会自动更新。

## 发布

仓库 Secrets 需要 MAC_SIGN_P12 与 MAC_SIGN_P12_PASSWORD。推送 `wails-v1.4.7` 等标签后，Wails 工作流生成 DMG 和 ZIP 并发布到 hyskr/easyread。签名证书必须跨版本保持一致；更换证书后需手动安装。当前为固定自签名，不包含 Apple 公证。
