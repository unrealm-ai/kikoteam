import { rm } from 'node:fs/promises';

// Rebuild from source so removed helpers cannot survive in a packaged dist.
await Promise.all([
  rm(new URL('../dist/', import.meta.url), { recursive: true, force: true }),
  rm(new URL('../vendor/pi-tools/lib/', import.meta.url), { recursive: true, force: true }),
]);
