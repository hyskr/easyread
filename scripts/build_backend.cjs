#!/usr/bin/env node
// Build the Go service and its native PDF dependencies. Never invokes Python.
const fs = require('node:fs');
const path = require('node:path');
const { execFileSync } = require('node:child_process');
const { bundlePoppler } = require('./bundle_poppler.cjs');
const root = path.resolve(__dirname, '..');
const out = path.join(root, 'build', 'backend');
const version = process.env.WAILS_VERSION || require('../package.json').version;
const targetOS = process.env.GOOS || ({ win32: 'windows', darwin: 'darwin', linux: 'linux' }[process.platform]);
const hostOS = ({ win32: 'windows', darwin: 'darwin', linux: 'linux' }[process.platform]);
if (targetOS !== hostOS) throw new Error('Build release packages on the target OS so native PDF tools can be validated.');
fs.mkdirSync(out, { recursive: true });
const executable = path.join(out, `easyread-backend${targetOS === 'windows' ? '.exe' : ''}`);
const temporary = `${executable}.building${targetOS === 'windows' ? '.exe' : ''}`;
try {
  execFileSync(process.env.GO || 'go', ['build', '-trimpath', `-ldflags=-s -w -X main.version=${version}`, '-o', temporary, './cmd/easyread'], {
    cwd: path.join(root, 'backend'), stdio: 'inherit', env: { ...process.env, CGO_ENABLED: '0' },
  });
  if (!process.env.EASYREAD_PDF_BIN && process.platform === 'darwin') process.env.EASYREAD_PDF_BIN = execFileSync('brew', ['--prefix', 'poppler'], {encoding:'utf8'}).trim() + '/bin';
  bundlePoppler(out);
  fs.renameSync(temporary, executable);
  console.log(`Go backend and PDF tools: ${out}`);
} finally { fs.rmSync(temporary, { force: true }); }
