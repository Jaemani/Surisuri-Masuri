import { access, readdir, readFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const projectRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const ignoredDirectories = new Set(['.git', 'node_modules'])

async function collectMarkdownFiles(directory) {
  const entries = await readdir(directory, { withFileTypes: true })
  const files = []

  for (const entry of entries) {
    if (entry.isDirectory() && ignoredDirectories.has(entry.name)) continue

    const fullPath = path.join(directory, entry.name)
    if (entry.isDirectory()) files.push(...(await collectMarkdownFiles(fullPath)))
    if (entry.isFile() && entry.name.endsWith('.md')) files.push(fullPath)
  }

  return files
}

function localTargets(markdown) {
  const targets = []
  const linkPattern = /\[[^\]]*\]\(([^)]+)\)/g

  for (const match of markdown.matchAll(linkPattern)) {
    const rawTarget = match[1].trim().replace(/^<|>$/g, '')
    if (
      rawTarget.startsWith('#') ||
      rawTarget.startsWith('http://') ||
      rawTarget.startsWith('https://') ||
      rawTarget.startsWith('mailto:')
    ) {
      continue
    }

    targets.push(rawTarget.split('#')[0])
  }

  return targets.filter(Boolean)
}

const failures = []
const markdownFiles = await collectMarkdownFiles(projectRoot)

for (const markdownFile of markdownFiles) {
  const markdown = await readFile(markdownFile, 'utf8')
  for (const target of localTargets(markdown)) {
    const resolved = path.resolve(path.dirname(markdownFile), target)
    try {
      await access(resolved)
    } catch {
      failures.push(`${path.relative(projectRoot, markdownFile)} -> ${target}`)
    }
  }
}

if (failures.length > 0) {
  process.stderr.write(`Broken local Markdown links:\n${failures.join('\n')}\n`)
  process.exitCode = 1
} else {
  process.stdout.write(`PASS ${markdownFiles.length} Markdown files have valid local links\n`)
}
