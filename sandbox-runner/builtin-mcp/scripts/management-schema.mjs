import { readFile, writeFile } from 'node:fs/promises';
import { format } from 'prettier';

const source = JSON.parse(
  await readFile(
    new URL('../contracts/json-schema/builtin/activity.json', import.meta.url),
    'utf8',
  ),
);
const target = new URL('../contracts/json-schema/builtin/activity-result.json', import.meta.url);
// The archive wraps two independent schemas in one document. Extracting the
// result gives its #/$defs references the correct standalone document root.
const generated = await format(JSON.stringify(source.result), { parser: 'json', printWidth: 100 });
if (process.argv.includes('--check')) {
  if ((await readFile(target, 'utf8')) !== generated)
    throw new Error('Regenerate the private activity schema with npm run generate.');
} else {
  await writeFile(target, generated);
}
