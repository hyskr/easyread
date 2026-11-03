import { zipSync, strToU8 } from 'fflate';

export async function exportXmind(root, filename) {
  let serial = 0;
  const resources = {}, pending = [];
  const topic = node => {
    const el = document.createElement('div'); el.innerHTML = node.content || '';
    const blocks = new Set(['P', 'DIV', 'LI', 'PRE', 'TR', 'BLOCKQUOTE']);
    const runs = [];
    function readText(node, style = {}) {
      if (node.nodeType === Node.TEXT_NODE) { if (node.textContent) runs.push({ text: node.textContent, ...style }); return; }
      if (node.nodeName === 'BR') { runs.push({ text: '\n' }); return; }
      const next = { ...style };
      if (['STRONG', 'B'].includes(node.nodeName)) next['fo:font-weight'] = 'bold';
      if (['EM', 'I'].includes(node.nodeName)) next['fo:font-style'] = 'italic';
      const decoration = new Set((next['fo:text-decoration'] || '').split(' ').filter(Boolean));
      if (node.nodeName === 'U') decoration.add('underline');
      if (['S', 'DEL', 'STRIKE'].includes(node.nodeName)) decoration.add('line-through');
      if (decoration.size) next['fo:text-decoration'] = [...decoration].join(' ');
      if (node.nodeName === 'CODE') next['fo:font-family'] = 'monospace';
      if (node.nodeName === 'MARK') next['fo:background-color'] = node.style.backgroundColor || '#fff59d';
      node.childNodes.forEach(child => readText(child, next));
      if (['TD', 'TH'].includes(node.nodeName)) runs.push({ text: '\t' });
      if (blocks.has(node.nodeName)) runs.push({ text: '\n' });
    }
    el.childNodes.forEach(child => readText(child));
    while (runs.length && !runs[0].text.trim()) runs.shift();
    while (runs.length && !runs[runs.length - 1].text.trim()) runs.pop();
    if (runs.length) { runs[0].text = runs[0].text.trimStart(); runs[runs.length - 1].text = runs[runs.length - 1].text.trimEnd(); }
    let text = runs.map(run => run.text).join('');
    if (el.querySelector('.pn-map-math')) text = text.replace(/([:：])\s*[。．.]$/, '$1');
    let length = text.length;
    const attributedTitle = runs.map(run => { const part = { ...run, text: run.text.slice(0, Math.max(0, length)) }; length -= run.text.length; return part; }).filter(run => run.text);
    const result = { id: 'topic-' + ++serial, class: 'topic', title: text };
    if (attributedTitle.some(run => Object.keys(run).length > 1)) result.attributedTitle = attributedTitle;
    const images = [...el.querySelectorAll('img[src]')];
    images.forEach((image, index) => {
      const separate = image.classList.contains('pn-map-math') && !!text || index > 0;
      const destination = !separate ? result : { id: 'topic-' + ++serial, class: 'topic', title: image.classList.contains('pn-map-math') ? '' : image.alt || '' };
      if (separate) { result.children ||= { attached: [] }; result.children.attached.push(destination); }
      pending.push((async () => {
        const response = await fetch(image.src);
        if (!response.ok) throw new Error('Image export failed: ' + response.status);
        let blob = await response.blob();
        const imageUrl = URL.createObjectURL(blob);
        const original = new Image();
        try {
          if (image.classList.contains('pn-map-math') && blob.type === 'image/svg+xml') {
            const svg = new DOMParser().parseFromString(await blob.text(), 'image/svg+xml');
            svg.documentElement.setAttribute('color', '#23211d');
            blob = new Blob([new XMLSerializer().serializeToString(svg)], {type:'image/svg+xml'});
          }
          const displayUrl = URL.createObjectURL(blob);
          original.src = displayUrl;
          try { await original.decode(); } finally { URL.revokeObjectURL(displayUrl); }
          const formula = image.classList.contains('pn-map-math');
          const sourceWidth = formula ? Number(image.getAttribute('width')) || original.naturalWidth : original.naturalWidth;
          const sourceHeight = formula ? Number(image.getAttribute('height')) || original.naturalHeight : original.naturalHeight;
          const scale = formula ? Math.min(1, 440 / sourceWidth) : Math.min(1, 320 / sourceWidth, 220 / sourceHeight);
          const width = Math.max(1, Math.round(sourceWidth * scale));
          const height = Math.max(1, Math.round(sourceHeight * scale));
          if (formula) {
            const canvas = document.createElement('canvas');
            canvas.width = width * 4; canvas.height = height * 4;
            canvas.getContext('2d').drawImage(original, 0, 0, canvas.width, canvas.height);
            blob = await new Promise((resolve, reject) => canvas.toBlob(value => value ? resolve(value) : reject(new Error('Formula image export failed')), 'image/png'));
          }
          const extension = { 'image/png': 'png', 'image/jpeg': 'jpg', 'image/gif': 'gif', 'image/webp': 'webp', 'image/svg+xml': 'svg' }[blob.type] || 'png';
          const path = 'resources/' + destination.id + '.' + extension;
          resources[path] = new Uint8Array(await blob.arrayBuffer());
          destination.image = {
            src: 'xap:' + path,
            width,
            height,
          };
        } finally { URL.revokeObjectURL(imageUrl); }
      })());
    });
    const link = el.querySelector('a[href]');
    if (link) {
      const href = link.getAttribute('href');
      if (href.startsWith('#b-')) {
        const PR = window.PR, hash = PR.state.paper.meta?.source_sha256;
        const query = new URLSearchParams(hash ? { sha256: hash } : { id: PR.pid });
        query.set('block', href.slice(1));
        result.href = (PR.deepLinkScheme || window.easyreadDesktop?.deepLinkScheme || 'easyread') + '://open?' + query;
      } else result.href = href;
    }
    const additionalSources = [...el.querySelectorAll('a[href^="#b-"]')].flatMap(link => (link.dataset.noteSources || link.getAttribute('href').slice(3)).split(' '));
    const distinctSources = [...new Set(additionalSources)];
    if (distinctSources.length > 1) {
      result.children ||= { attached: [] };
      for (const anchor of distinctSources) {
        const PR = window.PR, hash = PR.state.paper.meta?.source_sha256;
        const query = new URLSearchParams(hash ? { sha256: hash } : { id: PR.pid }); query.set('block', 'b-' + anchor);
        result.children.attached.push({ id: 'topic-' + ++serial, class: 'topic', title: PR.t('第 {page} 页', {page:PR.blockById[anchor]?.page || '?'}), href: (window.easyreadDesktop?.deepLinkScheme || 'easyread') + '://open?' + query });
      }
    }
    if (node.children?.length) result.children = { attached: [...(result.children?.attached || []), ...node.children.map(topic)] };
    return result;
  };
  const rootTopic = topic(root);
  rootTopic.structureClass = 'org.xmind.ui.logic.right';
  await Promise.all(pending);
  const files = {
    'content.json': [{ id: 'sheet-1', class: 'sheet', title: rootTopic.title, rootTopic }],
    'metadata.json': { creator: { name: 'EasyRead', version: '1.4.6' } },
    'manifest.json': { 'file-entries': { 'content.json': {}, 'metadata.json': {}, ...Object.fromEntries(Object.keys(resources).map(path => [path, {}])) } },
  };
  const data = zipSync({ ...resources, ...Object.fromEntries(Object.entries(files).map(([name, value]) => [name, strToU8(JSON.stringify(value))])) });
  const url = URL.createObjectURL(new Blob([data], { type: 'application/octet-stream' }));
  const link = document.createElement('a'); link.href = url; link.download = filename + '.xmind'; link.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
