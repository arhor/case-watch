#!/usr/bin/env node
/**
 * Bootstrap ADRs in a repo:
 * - create ADR directory
 * - create docs/adr/README.md (index) using a template
 * - create first ADR: "Adopt architecture decision records"
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
        dir: 'docs/adr',
        forceIndex: false,
        firstTitle: 'Adopt architecture decision records',
        firstStatus: 'accepted',
        deciders: '',
        technicalStory: '',
        strategy: 'numbered',
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
            case '--force-index':
                out.forceIndex = true;
                break;
            case '--first-title':
                out.firstTitle = next();
                break;
            case '--first-status':
                out.firstStatus = next();
                break;
            case '--deciders':
                out.deciders = next();
                break;
            case '--technical-story':
                out.technicalStory = next();
                break;
            case '--strategy':
                out.strategy = next();
                break;
            case '--json':
                out.json = true;
                break;
            case '--help':
            case '-h':
                process.stdout.write(
                    [
                        'Usage: node bootstrap-adr.js [options]',
                        '',
                        'Options:',
                        '  --repo-root <path>     Repo root (default: .)',
                        '  --dir <path>           ADR directory (fixed: docs/adr/)',
                        '  --force-index          Overwrite index file if it exists',
                        '  --first-title <text>   Title for initial ADR',
                        '  --first-status <text>  Status for initial ADR (default: accepted)',
                        '  --strategy numbered|auto  Filename strategy (default: numbered)',
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

    if (!['auto', 'numbered'].includes(out.strategy)) {
        die(`Invalid --strategy: ${out.strategy}`);
    }
    return out;
}

function writeIndex(indexFile, adrDirName, { force }) {
    if (fs.existsSync(indexFile) && !force) {
        return;
    }
    const templateText = loadTemplate('readme').replaceAll('{ADR_DIR}', adrDirName).trimEnd();
    const indexFileDir = path.dirname(indexFile);

    fs.mkdirSync(indexFileDir, { recursive: true });
    fs.writeFileSync(indexFile, `${templateText}\n`, 'utf8');
}

function generateFirstAdr({ title, status, date, deciders, adrDir }) {
    const deciderLine = normalizeNames(deciders);

    return `---
status: ${status}
date: ${date}
decision-makers: ${deciderLine}
---

# ${title}

## Context and Problem Statement

Architecture decisions in this project are made implicitly — through code, conversations, and tribal knowledge. When a new contributor (human or AI agent) joins the codebase, there is no record of *why* things are built the way they are. This makes it hard to:

- Understand whether a pattern is intentional or accidental
- Know if a past decision still applies or has been superseded
- Avoid relitigating decisions that were already carefully considered

We need a lightweight, version-controlled way to capture decisions where the code lives.

## Decision

Adopt Architecture Decision Records (ADRs) using the MADR 4.0 format, stored in \`${adrDir}/\`.

Conventions:
- One ADR per file, named \`{NNNN}_title-with-dashes.md\`
- New ADRs start as \`proposed\`, move to \`accepted\` or \`rejected\`
- Superseded ADRs link to their replacement
- ADRs are written to be self-contained — a coding agent should be able to read one and implement the decision without further context

## Consequences

* Good, because decisions are discoverable and version-controlled alongside the code
* Good, because new contributors (human or agent) can understand the "why" behind architecture choices
* Good, because the team builds a shared decision log that prevents relitigating settled questions
* Bad, because writing ADRs takes time — though a good ADR saves more time than it costs
* Neutral, because ADRs require periodic review to mark outdated decisions as deprecated or superseded

## Alternatives Considered

* No formal records: Continue making decisions in conversations and code comments. Rejected because context is lost and decisions get relitigated.
* Wiki or Notion pages: Capture decisions outside the repo. Rejected because they drift out of sync with the code and are not version-controlled.
* Lightweight RFCs: More heavyweight process with formal review cycles. Rejected as overkill for most decisions — ADRs can scale up to RFC-level detail when needed.

## More Information

* MADR: <https://adr.github.io/madr/>
* Michael Nygard, "Documenting Architecture Decisions": <https://cognitect.com/blog/2011/11/15/documenting-architecture-decisions>`;
}

function updateIndexFile(indexFile, { relLink, title, status, date }) {
    if (!fs.existsSync(indexFile)) {
        return;
    }
    let content = fs.readFileSync(indexFile, 'utf8');
    if (content.includes(relLink)) {
        return;
    }

    const entryLine = `- [${title}](${relLink}) (${status}, ${date})`;

    // Append after "## ADRs" heading if found, otherwise append at end
    const normalized = content.replace(/\r\n/g, '\n');
    const lines = normalized.split('\n');
    const headingIdx = lines.findIndex(l => /^##\s+ADRs\s*$/i.test(l));

    if (headingIdx !== -1) {
        // Insert after the heading (and any blank line after it)
        let insertAt = headingIdx + 1;
        while (insertAt < lines.length && lines[insertAt].trim() === '') {
            insertAt++;
        }
        lines.splice(insertAt, 0, entryLine);
    } else {
        lines.push(entryLine);
    }

    fs.writeFileSync(indexFile, lines.join('\n'), 'utf8');
}

function main() {
    const args = parseArgs(process.argv);
    const { repoRoot, adrDir } = resolveAdrPaths(args.repoRoot, args.dir);

    fs.mkdirSync(adrDir, { recursive: true });

    const indexFile = path.join(adrDir, 'README.md');
    const indexExistedBefore = fs.existsSync(indexFile);

    writeIndex(indexFile, args.dir, { force: args.forceIndex });

    const indexWritten = fs.existsSync(indexFile) && (!indexExistedBefore || args.forceIndex);

    // Create the first ADR as a filled-out decision (not a blank template).
    const relIndex = path.isAbsolute(indexFile)
        ? path.relative(repoRoot, indexFile)
        : indexFile;
    const today = todayISO();

    const firstAdrContent = generateFirstAdr({
        title: args.firstTitle,
        status: args.firstStatus,
        date: today,
        deciders: args.deciders,
        adrDir: args.dir,
    });

    const strategy = 'numbered';
    const firstAdrFilename = `0001_${slugify(args.firstTitle)}.md`;
    const firstAdrPath = path.join(adrDir, firstAdrFilename);
    // Bootstrap is repeatable and must preserve the existing first decision.
    const hasFirstAdr = fs.readdirSync(adrDir).some(name => /^0001_.*\.md$/.test(name));
    if (!hasFirstAdr) {
        fs.writeFileSync(firstAdrPath, `${firstAdrContent.trimEnd()}\n`, { encoding: 'utf8', flag: 'wx' });
    } else if (!fs.existsSync(firstAdrPath)) {
        die('ADR 0001 already exists with a different title; use new-adr.js for another decision');
    }

    // Update index
    const relLink = toPosix(path.relative(path.dirname(indexFile), firstAdrPath));
    updateIndexFile(indexFile, {
        relLink,
        title: args.firstTitle,
        status: args.firstStatus,
        date: today,
    });

    if (args.json) {
        const payload = {
            repoRoot,
            adrDir,
            adrDirRelPath: toPosix(path.relative(repoRoot, adrDir)),
            indexPath: indexFile,
            indexRelPath: toPosix(relIndex),
            indexExistedBefore,
            indexWritten,
            firstAdr: {
                createdAdrPath: firstAdrPath,
                createdAdrRelPath: toPosix(path.relative(repoRoot, firstAdrPath)),
                title: args.firstTitle,
                status: args.firstStatus,
                strategy,
                date: today,
            },
            date: today,
        };
        process.stdout.write(`${JSON.stringify(payload)}\n`);
        return;
    }

    process.stdout.write(`${firstAdrPath}\n`);
    process.stdout.write(`Bootstrapped ADRs at ${adrDir} (${today})\n`);
    process.stdout.write(`Index: ${indexFile}\n`);
}

main();
