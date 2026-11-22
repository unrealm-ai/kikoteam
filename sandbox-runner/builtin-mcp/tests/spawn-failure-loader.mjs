// Inject a real exec ENOENT only into the owned test supervisor.
import childProcess from 'node:child_process';
import { syncBuiltinESMExports } from 'node:module';

const original = childProcess.spawn;
childProcess.spawn = (command, args, options) =>
  original(command === '/bin/bash' ? '/nonexistent-builtin-test-bash' : command, args, options);
syncBuiltinESMExports();
