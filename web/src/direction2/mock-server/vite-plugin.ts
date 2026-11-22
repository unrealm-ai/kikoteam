import type { Plugin } from 'vite';
import { sharedMockBackend } from './handler';

export function d2MockServerPlugin(): Plugin {
  return {
    name: 'd2-local-mock-server',
    configureServer(server) {
      server.middlewares.use((req, res, next) => {
        const url = req.url || '';
        if (!url.startsWith('/api/')) {
          next();
          return;
        }
        const stream = req as unknown as {
          setEncoding?: (enc: string) => void;
          on: (event: string, cb: (arg?: unknown) => void) => void;
        };
        if (typeof stream.setEncoding === 'function') {
          stream.setEncoding('utf8');
        }
        const chunks: string[] = [];
        stream.on('data', chunk => {
          if (typeof chunk === 'string') {
            chunks.push(chunk);
          } else if (chunk !== undefined) {
            chunks.push(String(chunk));
          }
        });
        stream.on('end', async () => {
          try {
            const headers = (req as unknown as { headers?: Record<string, string | string[] | undefined> }).headers;
            const result = await sharedMockBackend.handle(req.method || 'GET', url, chunks.join(''), headers);
            res.statusCode = result.status;
            res.setHeader('Content-Type', 'application/json; charset=utf-8');
            res.setHeader('X-D2-Mock-Server', 'true');
            if (result.headers) {
              for (const [k, v] of Object.entries(result.headers)) {
                res.setHeader(k, v);
              }
            }
            res.end(JSON.stringify(result.body));
          } catch (err) {
            res.statusCode = 500;
            res.setHeader('Content-Type', 'application/json; charset=utf-8');
            res.end(JSON.stringify({ error: { code: 'MOCK_INTERNAL_ERROR', message: err instanceof Error ? err.message : 'Mock backend error' } }));
          }
        });
      });
    }
  };
}
