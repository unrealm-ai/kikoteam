import { createFileTools } from './files.js';
import { describeTool } from './definitions.js';

// Derived from the same pinned factories as the live server. Consumers may
// preseed these definitions, then replace the entire binding after discovery.
const tools = createFileTools('/workspace').map(describeTool);
process.stdout.write(JSON.stringify({ pi_version: '1.5.2', tools }, null, 2) + '\n');
