import { readdir, readFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const docsRoot = path.join(root, 'docs')
const failures = []

async function markdownFiles(directory) {
  const entries = await readdir(directory, { withFileTypes: true })
  const files = []
  for (const entry of entries) {
    const full = path.join(directory, entry.name)
    if (entry.isDirectory()) files.push(...(await markdownFiles(full)))
    else if (entry.name.endsWith('.md')) files.push(full)
  }
  return files
}

function relative(file) {
  return path.relative(root, file)
}

function githubSlug(value) {
  return value
    .trim()
    .toLowerCase()
    .replace(/[\u2000-\u206f\u2e00-\u2e7f'!"#$%&()*+,./:;<=>?@[\\\]^`{|}~]/g, '')
    .replace(/\s/g, '-')
}

function headingAnchors(markdown) {
  const anchors = new Set()
  const seen = new Map()
  for (const line of markdown.split('\n')) {
    const match = line.match(/^#{1,6}\s+(.+?)\s*#*$/)
    if (!match) continue
    const base = githubSlug(match[1])
    const count = seen.get(base) ?? 0
    anchors.add(count === 0 ? base : `${base}-${count}`)
    seen.set(base, count + 1)
  }
  return anchors
}

const files = await markdownFiles(docsRoot)
const contents = new Map(await Promise.all(files.map(async (file) => [file, await readFile(file, 'utf8')])))

// Local Markdown fragments must resolve to an actual heading, not merely a file.
for (const [file, markdown] of contents) {
  for (const match of markdown.matchAll(/\[[^\]]*\]\(([^)]+)\)/g)) {
    const raw = match[1].trim().replace(/^<|>$/g, '')
    if (!raw.includes('#') || /^(https?:|mailto:)/.test(raw)) continue
    const [targetPath, fragment] = raw.split('#', 2)
    if (!fragment) continue
    const target = targetPath ? path.resolve(path.dirname(file), targetPath) : file
    const targetMarkdown = contents.get(target)
    const decoded = decodeURIComponent(fragment).toLowerCase()
    const idPrefix = decoded.match(/^(?:evd|adr|upd|hr|inc|devfail)-[a-z0-9-]+?(?=--|$)/)?.[0]
    const hasIdHeading = idPrefix
      ? [...targetMarkdown?.matchAll(/^#{1,6}\s+((?:EVD|ADR|UPD|HR|INC|DEVFAIL)-[A-Z0-9-]+)/gm) ?? []].some(
          (heading) => heading[1].toLowerCase() === idPrefix,
        )
      : false
    if (targetMarkdown && !hasIdHeading && !headingAnchors(targetMarkdown).has(decoded)) {
      failures.push(`${relative(file)} -> missing anchor ${raw}`)
    }
  }
}

function filesIn(directory, prefix) {
  return [...contents.keys()].filter(
    (file) => path.dirname(file) === path.join(docsRoot, directory) && path.basename(file).startsWith(prefix),
  )
}

function checkDocumentFamily(directory, prefix, pattern, indexFile) {
  const definitions = new Map()
  const index = contents.get(path.join(docsRoot, indexFile)) ?? ''
  for (const file of filesIn(directory, prefix)) {
    const filename = path.basename(file)
    const idFromFilename = filename.match(pattern)?.[0]
    const idFromHeading = contents.get(file).match(new RegExp(`^#\\s+(${pattern.source})\\b`, 'm'))?.[1]
    const idFromMetadata = contents.get(file).match(
      new RegExp(`(?:^id:|ID:|업데이트 ID:)\\s*\`?(${pattern.source})\`?`, 'm'),
    )?.[1]
    const id = idFromHeading ?? idFromMetadata
    if (!idFromFilename || !id || idFromFilename !== id) {
      failures.push(`${relative(file)} -> filename/document ID mismatch (${idFromFilename ?? 'none'} vs ${id ?? 'none'})`)
      continue
    }
    if (definitions.has(id)) failures.push(`duplicate ${id}: ${relative(definitions.get(id))}, ${relative(file)}`)
    else definitions.set(id, file)
    if (!index.includes(`(${`./${filename}`})`)) failures.push(`${relative(file)} -> missing from ${indexFile}`)
  }
}

checkDocumentFamily('reports/human', 'HR-', /HR-\d{8}-\d{2}/, 'reports/human/README.md')
checkDocumentFamily('product-updates', 'UPD-', /UPD-\d{8}-\d{2}/, 'product-updates/README.md')

// Evidence definitions use a global canonical namespace.
const evidence = new Map()
for (const [file, markdown] of contents) {
  if (path.basename(file) === 'README.md' || path.basename(file).startsWith('_')) continue
  for (const match of markdown.matchAll(/^#{1,6}\s+(EVD-\d{8}-\d+)\b/gm)) {
    const id = match[1]
    if (!/^EVD-\d{8}-\d{3}$/.test(id)) failures.push(`${relative(file)} -> non-canonical evidence ID ${id}`)
    const previous = evidence.get(id)
    if (previous) failures.push(`duplicate ${id}: ${previous}, ${relative(file)}`)
    else evidence.set(id, relative(file))
  }
}

// Every linked Evidence ID must resolve to exactly one canonical definition.
for (const [file, markdown] of contents) {
  for (const match of markdown.matchAll(/\[([^\]]*EVD-\d{8}-\d{3}[^\]]*)\]\(([^)]+)\)/g)) {
    const id = match[1].match(/EVD-\d{8}-\d{3}/)?.[0]
    if (id && !evidence.has(id)) failures.push(`${relative(file)} -> undefined linked evidence ${id}`)
  }
}

// The 8-month plan has exactly two fixed reports per month and an immutable R01..R16 mapping.
const expectedFixed = [
  ['2026-05-15.md', 'R01'], ['2026-05-31.md', 'R02'],
  ['2026-06-15.md', 'R03'], ['2026-06-30.md', 'R04'],
  ['2026-07-15.md', 'R05'], ['2026-07-31.md', 'R06'],
  ['2026-08-15.md', 'R07'], ['2026-08-31.md', 'R08'],
  ['2026-09-15.md', 'R09'], ['2026-09-30.md', 'R10'],
  ['2026-10-15.md', 'R11'], ['2026-10-31.md', 'R12'],
  ['2026-11-15.md', 'R13'], ['2026-11-30.md', 'R14'],
  ['2026-12-15.md', 'R15'], ['2026-12-31.md', 'R16'],
]
for (const [filename, reportId] of expectedFixed) {
  const file = path.join(docsRoot, 'reports/fixed', filename)
  const markdown = contents.get(file)
  if (!markdown) failures.push(`missing fixed report reports/fixed/${filename}`)
  else {
    if (!markdown.includes(`보고서 ID: ${reportId}`)) failures.push(`${relative(file)} -> expected ${reportId}`)
    if (!markdown.includes(`계획 기준일: ${filename.slice(0, -3)}`)) failures.push(`${relative(file)} -> date mismatch`)
    if (!markdown.includes('실제 세션·발행일:')) failures.push(`${relative(file)} -> missing actual publication boundary`)
  }
}

if (failures.length) {
  process.stderr.write(`Documentation integrity failures:\n${failures.join('\n')}\n`)
  process.exitCode = 1
} else {
  process.stdout.write(`PASS ${files.length} documentation files have valid anchors, canonical IDs, and indexes\n`)
}
