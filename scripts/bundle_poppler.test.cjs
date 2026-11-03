const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const child = require('node:child_process');
const modulePath = require.resolve('./bundle_poppler.cjs');
const tools = ['pdfinfo', 'pdftoppm', 'pdftotext', 'pdftohtml'];

test('non-mac packaging rejects absent explicit native distribution', { skip: process.platform === 'darwin' }, () => {
  const old = process.env.EASYREAD_PDF_BIN; delete process.env.EASYREAD_PDF_BIN;
  try { assert.throws(() => require(modulePath).bundlePoppler('/unused'), /complete Poppler binary distribution/); }
  finally { if (old !== undefined) process.env.EASYREAD_PDF_BIN = old; }
});

test('macOS relocation recursively copies libraries and rewrites loaders', () => {
  const root = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), 'easyread-bundle-test-')));
  const bin = path.join(root, 'source/bin'); const lib = path.join(root, 'source/lib');
  fs.mkdirSync(bin, { recursive: true }); fs.mkdirSync(lib);
  for (const tool of tools) fs.writeFileSync(path.join(bin, tool), 'executable');
  const a = path.join(lib, 'liba.dylib'); const b = path.join(lib, 'libb.dylib');
  fs.writeFileSync(a, 'a\0/opt/homebrew/share/poppler\0'); fs.writeFileSync(b, 'b');
  const dataPrefix=path.join(root,'poppler-data');fs.mkdirSync(path.join(dataPrefix,'share/poppler'),{recursive:true});fs.writeFileSync(path.join(dataPrefix,'share/poppler/CMap'),'data');
  const originalPlatform = Object.getOwnPropertyDescriptor(process, 'platform');
  const oldDir = process.env.EASYREAD_PDF_BIN; const originalExec = child.execFileSync;
  const changes = [];
  try {
    Object.defineProperty(process, 'platform', { value: 'darwin' }); process.env.EASYREAD_PDF_BIN = bin;
    child.execFileSync = (cmd, args) => {
      if (cmd === 'brew' && args[0] === '--prefix') return dataPrefix;
      if (cmd === '/usr/bin/otool' && args[0] === '-L') {
        const file = args[1]; const isSource = file.startsWith(path.join(root, 'source'));
        const deps = isSource ? (tools.includes(path.basename(file)) ? [a] : (file === a ? [a, b] : [b])) : [];
        return `${file}:\n${deps.map(d => `\t${d} (compatibility version 1.0.0, current version 1.0.0)`).join('\n')}\n\t/usr/lib/libSystem.B.dylib (compatibility version 1.0.0, current version 1.0.0)\n`;
      }
      if (cmd === '/usr/bin/install_name_tool') changes.push(args);
      return '';
    };
    delete require.cache[modulePath]; const { bundlePoppler } = require(modulePath);
    const out = path.join(root, 'out'); bundlePoppler(out);
    assert.ok(fs.readFileSync(path.join(out, 'lib/liba.dylib'), 'utf8').includes('./share/poppler\0'));
    assert.ok(fs.existsSync(path.join(out,'share/poppler/CMap')));
    assert.equal(fs.readFileSync(path.join(out, 'lib/libb.dylib'), 'utf8'), 'b');
    assert.ok(changes.some(a => a[0] === '-change' && a[2] === '@loader_path/../lib/liba.dylib'));
    assert.ok(changes.some(a => a[0] === '-change' && a[2] === '@loader_path/libb.dylib'));
    assert.ok(fs.existsSync(path.join(out, 'PDF-THIRD-PARTY-NOTICES.txt')));
    assert.ok(fs.existsSync(path.join(out, 'fonts/fonts.conf')));
  } finally {
    Object.defineProperty(process, 'platform', originalPlatform); child.execFileSync = originalExec;
    if (oldDir === undefined) delete process.env.EASYREAD_PDF_BIN; else process.env.EASYREAD_PDF_BIN = oldDir;
    delete require.cache[modulePath]; fs.rmSync(root, { recursive: true, force: true });
  }
});
