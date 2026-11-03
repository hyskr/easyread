/* Preserve the rendered source structure while treating media as whole blocks. */
(function (PR) {
  "use strict";
  PR.quotePaperBlock = function (id) {
    const element = document.getElementById('b-' + id);
    if (!element) return;
    const range = document.createRange(); range.selectNodeContents(element);
    const html = PR.capturePaperQuote(element, range);
    PR.openPaperNote();
    PR.paperNoteEditor.insertSources([{ anchor: id, quote: PR.fullText(element), quoteHTML: html }]).catch(error => PR.toast(error.message));
  };
  PR.capturePaperQuote = function (el, range) {
    const block = el.closest('.blk'), type = PR.blockById[block.dataset.id]?.type;
    const holder = document.createElement('div');
    if (type === 'table' || type === 'figure') holder.appendChild(block.cloneNode(true));
    else {
      const selected = range.cloneRange();
      for (const [container, method] of [[range.startContainer, 'setStartBefore'], [range.endContainer, 'setEndAfter']]) {
        const parent = container.nodeType === Node.ELEMENT_NODE ? container : container.parentElement;
        const math = parent.closest('.katex');
        if (math && el.contains(math)) selected[method](math);
      }
      if (!el.contains(selected.startContainer)) selected.setStart(el, 0);
      if (!el.contains(selected.endContainer)) selected.setEnd(el, el.childNodes.length);
      holder.appendChild(selected.cloneContents());
      let ancestor = selected.commonAncestorContainer.nodeType === Node.ELEMENT_NODE ? selected.commonAncestorContainer : selected.commonAncestorContainer.parentElement;
      while (ancestor && ancestor !== el) {
        if (ancestor.matches('strong,b,em,i,u,s,del,code,a,mark,sub,sup')) {
          const wrapper = ancestor.cloneNode(false); wrapper.append(...holder.childNodes); holder.appendChild(wrapper);
        }
        ancestor = ancestor.parentElement;
      }
    }
    holder.querySelectorAll('.katex').forEach(math => {
      const latex = math.querySelector('annotation[encoding="application/x-tex"]')?.textContent;
      if (latex) { const span = document.createElement('span'); span.dataset.type = 'inline-math'; span.dataset.latex = latex; math.replaceWith(span); }
    });
    holder.querySelectorAll('button, script, style, .pgmark, .math-tag, .pn-source-links, .inline-notes, .stale-tag, .en-title, .num, .figure-translation-label, .fig-generate').forEach(node => node.remove());
    if (type === 'table' || type === 'figure') holder.querySelectorAll(PR.isEnEl(el) ? '.zh' : '.en').forEach(node => node.remove());
    holder.querySelectorAll('img').forEach(image => { image.src = new URL(image.getAttribute('src'), document.baseURI).href; image.removeAttribute('loading'); });
    return holder.innerHTML;
  };
})(window.PR);
