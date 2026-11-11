(function (PR) {
  'use strict';
  const bridge = window.easyreadDesktop;
  if (!bridge?.isWails) return;
  let timer, state = {}, pending = false;
  const active = () => PR.settingsState?.tab === 'updates' && PR.$('#settingsDlg')?.classList.contains('open');
  async function refresh() {
    clearTimeout(timer);
    if (!active()) return;
    try { state = await bridge.updateState(); paint(); }
    catch (error) { state.message = error.message; paint(); }
    if (active()) timer = setTimeout(refresh, 1000);
  }
  function paint() {
    const root = PR.$('[data-update-page]');
    if (!root) return;
    root.querySelector('[data-update-current]').textContent = state.current || '…';
    root.querySelector('[data-update-message]').textContent = state.message || PR.t('尚未检查更新');
    const progress = root.querySelector('progress'), bytes = root.querySelector('[data-update-bytes]');
    progress.hidden = !state.busy;
    if (state.total > 0) { progress.max = state.total; progress.value = state.received; bytes.textContent = Math.floor(state.received / state.total * 100) + '% · ' + (state.received / 1048576).toFixed(1) + ' / ' + (state.total / 1048576).toFixed(1) + ' MB'; }
    else { progress.removeAttribute('value'); bytes.textContent = state.received > 0 ? (state.received / 1048576).toFixed(1) + ' MB' : ''; }
    root.querySelector('[data-update-check]').disabled = pending || !!state.busy;
  }
  PR.settingsTabs.updates = {
    render() {
      clearTimeout(timer); timer = setTimeout(refresh, 0);
      return '<section data-update-page><h3>EasyRead Wails</h3><p>' + PR.t('当前版本') + ' <b data-update-current>…</b></p><p data-update-message role="status"></p><progress style="width:100%" hidden></progress><p data-update-bytes class="hint"></p><button class="btn primary" data-update-check>' + PR.t('检查更新') + '</button><p class="set-lead">' + PR.t('下载完成后会保存笔记，再重启安装。失败后可重新检查更新。') + '</p><a href="https://github.com/hyskr/easyread/releases" target="_blank" rel="noopener noreferrer">' + PR.t('查看发布记录') + '</a></section>';
    },
    async click(event) {
      if (!event.target.closest('[data-update-check]') || pending) return false;
      pending = true; state.message = PR.t('正在检查更新'); paint();
      try { await bridge.checkUpdate(); } catch (error) { state.message = error.message; }
      finally { pending = false; await refresh(); }
      return false;
    },
  };
})(window.PR);
