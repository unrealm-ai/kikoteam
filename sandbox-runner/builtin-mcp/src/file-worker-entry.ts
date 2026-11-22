import { createLogger } from './logging.js';

// Keep file execution in its own process while inheriting the Runner account.
// The parent supplies a restricted environment; no identity switch is needed.
try {
  const [cwd] = process.argv.slice(2);
  if (!cwd || process.argv.length !== 3) throw new Error('Invalid file worker configuration.');
  process.argv = [process.execPath, process.argv[1]!, '--cwd', cwd];
  await import('./main.js');
} catch {
  createLogger()(
    'worker.failed',
    'File worker failed',
    { error_code: 'worker_unavailable' },
    'ERROR',
  );
  process.exitCode = 1;
}
