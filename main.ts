// The platform launches `node /bot/main.ts`. This launcher runs the committed
// static Linux/amd64 forge binary; the binary binds its own ephemeral port,
// registers it with the env and waits for /done, exactly as the protocol requires.

import { spawnSync } from 'node:child_process';
import { existsSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));

function candidates(): string[] {
  const envBinary = process.env.MAKEFUN_FORGE_BINARY;
  const names = ['forge-linux-amd64', 'forge'];
  const list: string[] = [];
  if (envBinary) list.push(envBinary);
  for (const name of names) list.push(join(here, name));
  return list;
}

function main(): void {
  const binary = candidates().find((p) => existsSync(p));
  if (!binary) {
    process.stderr.write('forge: no committed forge binary found\n');
    process.exit(1);
  }
  const result = spawnSync(binary, [], { stdio: 'inherit' });
  if (result.error) {
    process.stderr.write(`forge: failed to start: ${result.error.message}\n`);
    process.exit(1);
  }
  process.exit(result.status ?? 1);
}

main();
