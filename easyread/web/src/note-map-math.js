import { mathjax } from 'mathjax-full/js/mathjax.js';
import { TeX } from 'mathjax-full/js/input/tex.js';
import { SVG } from 'mathjax-full/js/output/svg.js';
import { liteAdaptor } from 'mathjax-full/js/adaptors/liteAdaptor.js';
import { RegisterHTMLHandler } from 'mathjax-full/js/handlers/html.js';
import 'mathjax-full/js/input/tex/ams/AmsConfiguration.js';
import 'mathjax-full/js/input/tex/newcommand/NewcommandConfiguration.js';
import 'mathjax-full/js/input/tex/boldsymbol/BoldsymbolConfiguration.js';

const adaptor = liteAdaptor();
RegisterHTMLHandler(adaptor);
const document = mathjax.document('', { InputJax: new TeX({ packages: ['base', 'ams', 'newcommand', 'boldsymbol'] }), OutputJax: new SVG({ fontCache: 'none' }) });
const cache = new Map();

export function mapFormula(latex, display) {
  const color = window.getComputedStyle?.(window.document.documentElement).getPropertyValue('--ink').trim() || '#23211d';
  const key = JSON.stringify([latex, display, color]);
  if (cache.has(key)) return cache.get(key);
  const node = document.convert(latex, { display, em: 14, ex: 7, containerWidth: 440 });
  const svg = adaptor.firstChild(node);
  adaptor.setAttribute(svg, 'color', color);
  const width = Math.max(1, parseFloat(adaptor.getAttribute(svg, 'width')) * 7);
  const height = Math.max(1, parseFloat(adaptor.getAttribute(svg, 'height')) * 7);
  const scale = Math.min(1, 440 / width);
  const source = 'data:image/svg+xml;charset=utf-8,' + encodeURIComponent(adaptor.outerHTML(svg));
  const html = '<img class="pn-map-math" src="' + source + '" alt="' + window.PR.esc(latex) + '" width="' + Math.ceil(width * scale) + '" height="' + Math.ceil(height * scale) + '">';
  cache.set(key, html);
  return html;
}
