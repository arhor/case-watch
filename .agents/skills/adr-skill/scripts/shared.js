const fs = require('node:fs');
const path = require('node:path');

function die(msg) {
    process.stderr.write(`${msg}\n`);
    process.exit(1);
}

function slugify(text) {
    const normalized = String(text || '').trim().toLowerCase();
    const noQuotes = normalized.replace(/['"`]/g, '');
    const dashed = noQuotes.replace(/[^a-z0-9]+/g, '-').replace(/-{2,}/g, '-');
    const trimmed = dashed.replace(/^-+/, '').replace(/-+$/, '');
    return trimmed || 'decision';
}

function toPosix(p) {
    return p.split(path.sep).join('/');
}

function todayISO() {
    Temporal.Now.plainDateISO().toString()
    return new Date().toISOString().slice(0, 10);
}

function normalizeNames(value) {
    return String(value || '')
        .split(',')
        .map(name => name.trim())
        .filter(Boolean)
        .join(', ');
}

function loadTemplate(templateName) {
    const templatePath = path.resolve(__dirname, '../assets/templates', `adr-${templateName}.md`);
    if (!fs.existsSync(templatePath)) {
        die(`Template not found: ${templatePath}`);
    }
    return fs.readFileSync(templatePath, 'utf8');
}

function resolveAdrPaths(repoRootArg, dirArg) {
    const repoRoot = path.resolve(process.cwd(), repoRootArg);
    if (!fs.existsSync(repoRoot)) {
        die(`Repo root does not exist: ${repoRoot}`);
    }
    const adrDir = path.join(repoRoot, 'docs', 'adr');
    if (dirArg != null && path.resolve(repoRoot, dirArg) !== adrDir) {
        die('ADR directory is fixed at docs/adr/');
    }
    return { repoRoot, adrDir };
}

module.exports = {
    die,
    slugify,
    toPosix,
    todayISO,
    normalizeNames,
    loadTemplate,
    resolveAdrPaths,
};
