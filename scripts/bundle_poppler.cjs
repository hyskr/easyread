#!/usr/bin/env node
// Bundle native PDF utilities; macOS dylibs are relocated before signing.
const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');
const { execFileSync } = require('node:child_process');
const tools = ['pdfinfo', 'pdftoppm', 'pdftotext', 'pdftohtml'];
function run(cmd, args) { return execFileSync(cmd, args, { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim(); }
function exists(p) { try { return fs.statSync(p).isFile(); } catch { return false; } }
function findTool(name, dir) {
  const suffix = process.platform === 'win32' ? '.exe' : '';
  if (dir) { const p = path.join(dir, name + suffix); if (exists(p)) return fs.realpathSync(p); throw new Error(`Missing ${p}`); }
  const p = run('/usr/bin/which', [name]); return fs.realpathSync(p);
}
function dependencies(file) {
  return run('/usr/bin/otool', ['-L', file]).split('\n').slice(1).map(s => s.trim().replace(/ \(compatibility version.*$/, '')).filter(Boolean);
}
function rpaths(file) {
  const lines = run('/usr/bin/otool', ['-l', file]).split('\n'); const result = [];
  for (let i = 0; i < lines.length; i++) if (lines[i].trim() === 'cmd LC_RPATH') {
    const m = lines.slice(i + 1, i + 5).join('\n').match(/\bpath (.*?) \(offset/); if (m) result.push(m[1]);
  }
  return result;
}
function resolveDep(name, owner, entry, inherited = []) {
  if (path.isAbsolute(name)) return name;
  const expand = v => v.replace('@loader_path', path.dirname(owner)).replace('@executable_path', path.dirname(entry));
  if (name.startsWith('@loader_path') || name.startsWith('@executable_path')) return expand(name);
  if (name.startsWith('@rpath/')) {
    const suffix = name.slice(7);
    for (const r of [...rpaths(owner), ...inherited, path.join(path.dirname(owner), '../lib')]) {
      const p = path.join(expand(r), suffix); if (exists(p)) return p;
    }
  }
  throw new Error(`Cannot resolve ${name} referenced by ${owner}`);
}
function systemLibrary(name) { return name.startsWith('/usr/lib/') || name.startsWith('/System/Library/'); }
function copyLicenseFiles(prefix, destination) {
  const allowed = /^(copying|copyright|licen[sc]e|notice|authors)([._-].*)?$/i;
  const walk = (dir, relative = '', depth = 0) => {
    if (depth > 6 || !fs.existsSync(dir)) return;
    for (const item of fs.readdirSync(dir, { withFileTypes: true })) {
      const rel = path.join(relative, item.name); const src = path.join(dir, item.name);
      if (item.isDirectory() && !['bin', 'lib', 'include', 'libexec'].includes(item.name)) walk(src, rel, depth + 1);
      else if (item.isFile() && allowed.test(item.name) && fs.statSync(src).size < 4 * 1024 * 1024) {
        const dest = path.join(destination, rel); fs.mkdirSync(path.dirname(dest), { recursive: true }); fs.copyFileSync(src, dest);
      }
    }
  };
  walk(prefix);
}
function relocatePopplerData(file) {
  const data = fs.readFileSync(file); const bytes = data.toString('latin1');
  const matches = [...bytes.matchAll(/\/(?:opt|usr\/local)[A-Za-z0-9_./@+-]*\/share\/poppler(?=\x00)/g)];
  const replacement = Buffer.from('./share/poppler');
  for (const match of matches) {
    if (replacement.length > match[0].length) throw new Error('Poppler data path cannot be relocated in place');
    data.fill(0, match.index, match.index + match[0].length);
    replacement.copy(data, match.index);
  }
  if (matches.length) fs.writeFileSync(file, data);
  return matches.length;
}
function bundleMac(stage, sources) {
  const bin = path.join(stage, 'bin'); const lib = path.join(stage, 'lib'); fs.mkdirSync(bin); fs.mkdirSync(lib);
  const registry = new Map(); const names = new Map(); const packages = new Map();
  function register(source, entry, inherited = [], isEntry = false) {
    const real = fs.realpathSync(source); if (registry.has(real)) return registry.get(real);
    let base = path.basename(real);
    if (isEntry) base = path.basename(entry);
    if (!isEntry && names.has(base) && names.get(base) !== real) base = `${path.parse(base).name}-${crypto.createHash('sha256').update(real).digest('hex').slice(0, 8)}${path.extname(base)}`;
    names.set(base, real); const dest = path.join(isEntry ? bin : lib, base); registry.set(real, dest);
    fs.copyFileSync(real, dest); fs.chmodSync(dest, 0o755);
    const cellar = real.match(/^(.*\/Cellar\/([^/]+)\/[^/]+)\//); if (cellar) packages.set(cellar[2], cellar[1]);
    try { run('/usr/bin/codesign', ['--remove-signature', dest]); } catch { /* unsigned input */ }
    const arch = process.env.GOARCH || (process.arch === 'arm64' ? 'arm64' : 'amd64');
    run('/usr/bin/lipo', [dest, '-verify_arch', arch === 'amd64' ? 'x86_64' : arch]);
    const search = [...rpaths(real).map(v => v.replace('@loader_path', path.dirname(real)).replace('@executable_path', path.dirname(entry))), ...inherited];
    for (const dep of dependencies(real)) {
      if (systemLibrary(dep)) continue;
      const resolved = resolveDep(dep, real, entry, search);
      if (fs.realpathSync(resolved) === real) continue; // dylib install ID
      const target = register(resolved, entry, search, false);
      const relative = path.relative(path.dirname(dest), target).split(path.sep).join('/');
      run('/usr/bin/install_name_tool', ['-change', dep, `@loader_path/${relative}`, dest]);
    }
    if (!isEntry) run('/usr/bin/install_name_tool', ['-id', `@rpath/${path.basename(dest)}`, dest]);
    return dest;
  }
  for (const source of sources) register(source, source, [], true);
  // Poppler does not expose a runtime data-directory environment override.
  // Relocate only the NUL-terminated resource-path constants in copied binaries,
  // without changing Mach-O offsets; all changed binaries are re-signed below.
  let dataPrefix;
  try { dataPrefix = run('brew', ['--prefix', 'poppler-data']); } catch { dataPrefix = run('brew', ['--prefix']); }
  const dataDir = path.join(dataPrefix, 'share/poppler');
  if (!fs.existsSync(dataDir)) throw new Error('Missing poppler-data CMaps. Install brew install poppler-data.');
  fs.cpSync(dataDir, path.join(stage, 'share/poppler'), { recursive: true, dereference: true });
  packages.set('poppler-data', fs.realpathSync(dataPrefix));
  let relocatedData = 0;
  for (const dest of registry.values()) relocatedData += relocatePopplerData(dest);
  if (!relocatedData) throw new Error('Could not relocate the compiled Poppler data path; refusing a non-portable macOS bundle.');
  // Ensure no dependency on the build host's Homebrew cellar remains.
  for (const dest of registry.values()) {
    for (const dep of dependencies(dest)) if (!systemLibrary(dep) && !dep.startsWith('@loader_path/') && !dep.startsWith('@rpath/')) throw new Error(`Unrelocated dependency: ${dest}: ${dep}`);
    run('/usr/bin/codesign', ['--force', '--sign', '-', dest]);
  }
  const fonts = path.join(stage, 'fonts'); fs.mkdirSync(fonts);
  fs.writeFileSync(path.join(fonts, 'fonts.conf'), '<?xml version="1.0"?><!DOCTYPE fontconfig SYSTEM "urn:fontconfig:fonts.dtd"><fontconfig><dir>/System/Library/Fonts</dir><dir>/Library/Fonts</dir><dir>~/Library/Fonts</dir><cachedir prefix="xdg">fontconfig</cachedir><config><rescan><int>30</int></rescan></config></fontconfig>');
  const licenses = path.join(stage, 'licenses'); fs.mkdirSync(licenses);
  const records = [];
  for (const [name, prefix] of packages) {
    let info;
    try { info = JSON.parse(run('brew', ['info', '--json=v2', name])).formulae[0]; } catch { info = { name, installed_prefix: prefix }; }
    records.push(info); copyLicenseFiles(prefix, path.join(licenses, name));
  }
  fs.writeFileSync(path.join(licenses, 'homebrew-source-manifest.json'), JSON.stringify(records, null, 2));
  return { packages: records.map(p => ({ name: p.name, license: p.license, homepage: p.homepage, source: p.urls?.stable })) };
}
function bundleExplicit(stage, directory) {
  // For Windows/Linux the provided directory must be a complete redistributable
  // tool distribution, including its DLLs/shared libraries and license files.
  fs.cpSync(directory, path.join(stage, 'bin'), { recursive: true, dereference: true });
  const siblingLib = path.join(path.dirname(directory), 'lib');
  const siblingLicenses = path.join(path.dirname(directory), 'licenses');
  const siblingShare = path.join(path.dirname(directory), 'share');
  if (path.basename(directory) === 'bin' && fs.existsSync(siblingShare)) fs.cpSync(siblingShare, path.join(stage, 'share'), { recursive: true, dereference: true });
  if (path.basename(directory) === 'bin' && fs.existsSync(siblingLib)) fs.cpSync(siblingLib, path.join(stage, 'lib'), { recursive: true, dereference: true });
  if (fs.existsSync(siblingLicenses)) fs.cpSync(siblingLicenses, path.join(stage, 'licenses'), { recursive: true, dereference: true });
  const licenseDirs = [path.join(stage, 'licenses'), path.join(stage, 'bin', 'licenses')];
  const hasLicense = licenseDirs.some(d => fs.existsSync(d) && fs.readdirSync(d).length) || fs.readdirSync(path.join(stage, 'bin')).some(n => /^(license|copying|copyright)/i.test(n));
  if (!hasLicense) throw new Error('The EASYREAD_PDF_BIN distribution must include licenses/ or COPYING/LICENSE files.');
  return { source: 'EASYREAD_PDF_BIN complete native distribution' };
}
function bundlePoppler(out) {
  const explicit = process.env.EASYREAD_PDF_BIN && path.resolve(process.env.EASYREAD_PDF_BIN);
  if (process.platform !== 'darwin' && !explicit) throw new Error('Packaging requires EASYREAD_PDF_BIN pointing to a complete Poppler binary distribution with libraries and licenses. A system installation alone is not a portable package.');
  const sources = tools.map(t => findTool(t, explicit));
  const parent = path.dirname(out); fs.mkdirSync(parent, { recursive: true });
  const stage = fs.mkdtempSync(path.join(parent, '.poppler-'));
  try {
    const manifest = process.platform === 'darwin' ? bundleMac(stage, sources) : bundleExplicit(stage, explicit);
    for (const name of tools) {
      const exe = path.join(stage, 'bin', name + (process.platform === 'win32' ? '.exe' : ''));
      execFileSync(exe, ['-v'], { cwd: stage, stdio: 'pipe', env: { ...process.env, LD_LIBRARY_PATH: path.join(stage, 'lib'), DYLD_LIBRARY_PATH: '', DYLD_FALLBACK_LIBRARY_PATH: '', FONTCONFIG_FILE: process.platform === 'darwin' ? path.join(stage, 'fonts/fonts.conf') : (process.env.FONTCONFIG_FILE || '') } });
    }
    fs.writeFileSync(path.join(stage, 'PDF-THIRD-PARTY-NOTICES.txt'), 'EasyRead bundles Poppler command-line utilities and their runtime libraries.\nPoppler: https://poppler.freedesktop.org/ (GPL-2.0-or-later; see included licenses and source manifest).\nNative executables are separate programs invoked by the Go backend.\nDistributors must retain copyright/license notices and comply with source-distribution obligations for each component.\n' + JSON.stringify(manifest, null, 2) + '\n');
    fs.mkdirSync(out, { recursive: true });
    for (const entry of fs.readdirSync(stage)) { const dest = path.join(out, entry); fs.rmSync(dest, { recursive: true, force: true }); fs.renameSync(path.join(stage, entry), dest); }
  } finally { fs.rmSync(stage, { recursive: true, force: true }); }
}
module.exports = { bundlePoppler, resolveDep, systemLibrary, relocatePopplerData };
if (require.main === module) bundlePoppler(path.resolve(process.argv[2] || path.join(__dirname, '../build/backend')));
