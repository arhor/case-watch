#!/usr/bin/env node
/**
 * Create a new ADR markdown file using fixed ADR conventions and a template.
 *
 * Design goals:
 * - Safe defaults (docs/adr directory + sequential numbering)
 * - No external deps
 * - Works even if the repo has no ADRs yet
 */

const fs = require('node:fs');
const path = require('node:path');
const {
    die,
    slugify,
    toPosix,
    todayISO,
    normalizeNames,
    loadTemplate,
    resolveAdrPaths,
} = require('./shared');

function parseArgs(argv) {
    const out = {
        repoRoot: '.',
        dir: null,
        noCreateDir: false,
        title: null,
        status: 'proposed',
        template: 'simple',
        strategy: 'numbered',
        deciders: '',
        consulted: '',
        informed: '',
        technicalStory: '',
        chosenOption: '',
        updateIndex: false,
        json: false,
    };

    for (let i = 2; i < argv.length; i++) {
        const arg = argv[i];
        const next = () => {
            if (i + 1 >= argv.length) {
                die(`Missing value for ${arg}`);
            }
            return argv[++i];
        };

        switch (arg) {
            case '--repo-root':
                out.repoRoot = next();
                break;
            case '--dir':
                out.dir = next();
                break;
            case '--no-create-dir':
                out.noCreateDir = true;
                break;
            case '--title':
                out.title = next();
                break;
            case '--status':
                out.status = next();
                break;
            case '--template':
                out.template = next();
                break;
            case '--strategy':
                out.strategy = next();
                break;
            case '--deciders':
                out.deciders = next();
                break;
            case '--consulted':
                out.consulted = next();
                break;
            case '--informed':
                out.informed = next();
                break;
            case '--technical-story':
                out.technicalStory = next();
                break;
            case '--chosen-option':
                out.chosenOption = next();
                break;
            case '--update-index':
                out.updateIndex = true;
                break;
            case '--json':
                out.json = true;
                break;
            case '--help':
            case '-h':
                process.stdout.write(
                    [
                        'Usage: node new-adr.js --title "Choose database" [options]',
                        '',
                        'Options:',
                        '  --repo-root <path>     Repo root (default: .)',
                        '  --dir <path>           ADR directory (fixed: docs/adr/)',
                        '  --no-create-dir        Do not create ADR directory if missing',
                        '  --status <value>       ADR status (default: proposed)',
                        '  --template simple|madr Template (default: simple)',
                        '  --strategy numbered|auto  Filename strategy (default: numbered)',
                        '  --deciders "a,b"      Deciders list',
                        '  --consulted "a,b"     Consulted experts (RACI)',
                        '  --informed "a,b"      Informed stakeholders (RACI)',
                        '  --technical-story <x>  Issue/ticket/PR link or short ref',
                        '  --chosen-option <x>    MADR template: chosen option label',
                        '  --update-index         Update docs/adr/README.md',
                        '  --json                 Output machine-readable JSON (default: off)',
                        '',
                    ].join('\n'),
                );
                process.exit(0);
                break;
            default:
                die(`Unknown arg: ${arg}`);
                break;
        }
    }

    if (!out.title) {
        die('Missing required --title');
    }
    if (!['simple', 'madr'].includes(out.template)) {
        die(`Invalid --template: ${out.template}`);
    }
    if (!['auto', 'numbered'].includes(out.strategy)) {
        die(`Invalid --strategy: ${out.strategy}`);
    }
    return out;
}

function listMdFiles(dir) {
    try {
        return fs.readdirSync(dir, { withFileTypes: true })
            .filter(e => e.isFile() && e.name.toLowerCase().endsWith('.md'))
            .map(e => e.name);
    } catch {
        return [];
    }
}

function nextAdrNumber(adrDir) {
    const numbers = listMdFiles(adrDir)
        .map(name => /^(\d{4})_.*\.md$/.exec(name))
        .filter(Boolean)
        .map(match => Number(match[1]));

    const next = Math.max(0, ...numbers) + 1;
    if (next > 9999) {
        die('ADR numbering exhausted (maximum 9999)');
    }
    return String(next).padStart(4, '0');
}

function renderTemplate(raw, vars) {
    // Handle YAML front matter placeholders (quoted and unquoted)
    let out = raw;

    // YAML front matter fields — replace the whole placeholder pattern
    // e.g. status: "{proposed | accepted | ...}" → status: proposed
    out = out.replace(/^(status:\s*)["']?\{[^}]*}["']?\s*$/m, `$1${vars.status}`);
    out = out.replace(/^(date:\s*)\{[^}]*}\s*$/m, `$1${vars.date}`);
    out = out.replace(/^(decision-makers:\s*)["']?\{[^}]*}["']?\s*$/m, `$1${vars.deciders || ''}`);

    // consulted / informed: replace if a value was provided, otherwise remove the
    // entire line so we don't leak placeholder text like "{list everyone...}"
    if (vars.consulted) {
        out = out.replace(/^(consulted:\s*)["']?\{[^}]*}["']?\s*$/m, `$1${vars.consulted}`);
    } else {
        out = out.replace(/^consulted:\s*["']?\{[^}]*}["']?\s*\n/m, '');
    }
    if (vars.informed) {
        out = out.replace(/^(informed:\s*)["']?\{[^}]*}["']?\s*$/m, `$1${vars.informed}`);
    } else {
        out = out.replace(/^informed:\s*["']?\{[^}]*}["']?\s*\n/m, '');
    }

    // Replace MADR-style heading placeholder
    out = out.replace(/^(#\s+)\{short title[^}]*}\s*$/m, `$1${vars.title}`);

    // Inline placeholders (title in heading, etc.)
    out = out
        .replaceAll('{TITLE}', vars.title)
        .replaceAll('{STATUS}', vars.status)
        .replaceAll('{DATE}', vars.date)
        .replaceAll('{DECIDERS}', vars.deciders)
        .replaceAll('{TECHNICAL_STORY}', vars.technicalStory)
        .replaceAll('{CHOSEN_OPTION}', vars.chosenOption);

    return out;
}

function insertIndexEntryUnderHeading(lines, headingRegex, entryLine) {
    // Returns { lines, inserted }
    const headingIndex = lines.findIndex(l => headingRegex.test(l));
    if (headingIndex === -1) {
        return {lines, inserted: false};
    }

    let sectionEnd = lines.length;
    for (let i = headingIndex + 1; i < lines.length; i++) {
        if (/^##\s+/.test(lines[i])) {
            sectionEnd = i;
            break;
        }
    }

    // Prefer inserting at end of list in this section if there is a list.
    let lastListItem = -1;
    for (let i = sectionEnd - 1; i > headingIndex; i--) {
        if (/^[-*]\s+/.test(lines[i])) {
            lastListItem = i;
            break;
        }
    }

    const insertAt = lastListItem !== -1 ? lastListItem + 1 : sectionEnd;
    const out = [...lines];

    // Ensure there's a blank line after the heading if we're inserting immediately after it.
    if (insertAt === headingIndex + 1 && out[insertAt] !== '') {
        out.splice(insertAt, 0, '');
    }

    out.splice(insertAt, 0, entryLine);
    return { lines: out, inserted: true };
}

function updateIndex(indexFile, { relLink, title, status, date }) {
    const content = fs.existsSync(indexFile)
        ? fs.readFileSync(indexFile, 'utf8')
        : '# ADR Log\n\n';

    if (content.includes(relLink)) {
        return false;
    }

    const normalized = content.replace(/\r\n/g, '\n');
    const hadTrailingNewline = normalized.endsWith('\n');

    // Normalize away the trailing empty split element so insertion math is sane.
    let lines = normalized.split('\n');
    if (hadTrailingNewline && lines.length > 0 && lines[lines.length - 1] === '') {
        lines = lines.slice(0, -1);
    }
    const entryLine = `- [${title}](${relLink}) (${status}, ${date})`;

    // Prefer inserting under "## ADRs" if it exists, otherwise append at EOF.
    const result = insertIndexEntryUnderHeading(lines, /^##\s+ADRs\s*$/i, entryLine);
    const nextLines = result.inserted ? result.lines : [...lines, entryLine];

    let next = nextLines.join('\n');
    if (hadTrailingNewline) {
        next += '\n';
    }

    fs.mkdirSync(path.dirname(indexFile), { recursive: true });
    fs.writeFileSync(indexFile, next, 'utf8');

    return true;
}

function main() {
    const args = parseArgs(process.argv);
    const { repoRoot, adrDir } = resolveAdrPaths(args.repoRoot, args.dir || null);

    if (!fs.existsSync(adrDir)) {
        if (args.noCreateDir) {
            die(`ADR directory does not exist: ${adrDir}`);
        }
        fs.mkdirSync(adrDir, { recursive: true });
    }

    const strategy = 'numbered';
    const title = String(args.title).trim();
    const slug = slugify(title);
    const today = todayISO();
    const filename = `${nextAdrNumber(adrDir)}_${slug}.md`;
    const out = path.join(adrDir, filename);
    const deciders = normalizeNames(args.deciders);
    const consulted = normalizeNames(args.consulted);
    const informed = normalizeNames(args.informed);
    const raw = loadTemplate(args.template);
    const rendered = renderTemplate(raw, {
        title,
        status: String(args.status).trim(),
        date: today,
        deciders,
        consulted,
        informed,
        technicalStory: String(args.technicalStory || '').trim(),
        chosenOption: String(args.chosenOption || '').trim(),
    });

    fs.writeFileSync(out, `${rendered.trimEnd()}\n`, { encoding: 'utf8', flag: 'wx' });

    let updatedIndexPath = null;
    let indexChanged = false;

    if (args.updateIndex) {
        const indexFile = path.join(adrDir, 'README.md');

        const relLink = toPosix(path.relative(path.dirname(indexFile), out));
        indexChanged = updateIndex(indexFile, {
            relLink,
            title,
            status: String(args.status).trim(),
            date: today,
        });
        updatedIndexPath = indexFile;
    }

    if (args.json) {
        const payload = {
            repoRoot,
            adrDir,
            createdAdrPath: out,
            createdAdrRelPath: toPosix(path.relative(repoRoot, out)),
            title,
            status: String(args.status).trim(),
            template: args.template,
            strategy,
            date: today,
            indexUpdated: Boolean(updatedIndexPath),
            indexChanged,
            indexPath: updatedIndexPath,
            indexRelPath: updatedIndexPath
                ? toPosix(path.relative(repoRoot, updatedIndexPath))
                : null,
        };
        process.stdout.write(`${JSON.stringify(payload)}\n`);
    } else {
        process.stdout.write(`${out}\n`);
    }
}

main();
