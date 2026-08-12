#!/usr/bin/env node
import { readFile } from 'node:fs/promises';

import { mapLegacyDevice, mapLegacyRepair, mapLegacyUser, reconcileImport } from './index.js';

const [manifestPath] = process.argv.slice(2);
if (!manifestPath) {
  process.stderr.write('Usage: node src/cli.js <dry-run-manifest.json>\n');
  process.exitCode = 2;
} else {
  const manifest = JSON.parse(await readFile(manifestPath, 'utf8'));
  const context = { runId: manifest.runId, sourceSystem: manifest.sourceSystem };
  const userCrosswalk = new Map(Object.entries(manifest.userCrosswalk ?? {}));
  const deviceCrosswalk = new Map(Object.entries(manifest.deviceCrosswalk ?? {}));
  const records = [
    ...(manifest.users ?? []).map((raw) => mapLegacyUser(context, raw)),
    ...(manifest.vehicles ?? []).map((raw) => mapLegacyDevice(context, raw, userCrosswalk)),
    ...(manifest.repairs ?? []).map((raw) => mapLegacyRepair(context, raw, deviceCrosswalk)),
  ];
  process.stdout.write(`${JSON.stringify({ reconciliation: reconcileImport(records), records }, null, 2)}\n`);
}
