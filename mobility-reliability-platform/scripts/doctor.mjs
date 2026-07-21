import { access, readFile } from 'node:fs/promises'
import { constants } from 'node:fs'
import os from 'node:os'
import path from 'node:path'

async function isExecutable(filePath) {
  try {
    await access(filePath, constants.X_OK)
    return true
  } catch {
    return false
  }
}

async function findOnPath(command) {
  const pathEntries = (process.env.PATH ?? '').split(path.delimiter).filter(Boolean)
  for (const entry of pathEntries) {
    const candidate = path.join(entry, command)
    if (await isExecutable(candidate)) return candidate
  }
  return null
}

async function detectWsl() {
  try {
    const version = await readFile('/proc/version', 'utf8')
    return /microsoft|wsl/i.test(version)
  } catch {
    return false
  }
}

const wsl = await detectWsl()
const workspace = process.cwd()
const tools = {
  java: await findOnPath('java'),
  adb: await findOnPath('adb'),
  adbExe: await findOnPath('adb.exe'),
  go: await findOnPath('go'),
}

const checks = [
  {
    label: 'WSL2 environment',
    status: wsl ? 'PASS' : 'INFO',
    detail: wsl ? `${os.release()} detected` : 'not running under WSL',
  },
  {
    label: 'Linux filesystem workspace',
    status: workspace.startsWith('/mnt/') ? 'WARN' : 'PASS',
    detail: workspace,
  },
  {
    label: 'Node.js',
    status: 'PASS',
    detail: process.version,
  },
  {
    label: 'Java for Firebase Emulator',
    status: tools.java ? 'PASS' : 'WARN',
    detail: tools.java ?? 'java not found on PATH',
  },
  {
    label: 'Android Debug Bridge',
    status: tools.adb || tools.adbExe ? 'PASS' : 'WARN',
    detail: tools.adb ?? tools.adbExe ?? 'adb and adb.exe not found on PATH',
  },
  {
    label: 'Go toolchain',
    status: tools.go ? 'PASS' : 'WARN',
    detail: tools.go ?? 'go not found on PATH; required before telemetry gateway work',
  },
]

for (const check of checks) {
  process.stdout.write(`${check.status.padEnd(4)} ${check.label}: ${check.detail}\n`)
}

if (wsl && !tools.adb && !tools.adbExe) {
  process.stdout.write(
    '\nNEXT Install Android platform-tools on Windows or WSL, then follow docs/development/WSL_RUNBOOK.md.\n'
  )
}

if (wsl) {
  process.stdout.write(
    'INFO iOS native builds are not available in WSL; use EAS development builds and a physical iPhone.\n'
  )
}
