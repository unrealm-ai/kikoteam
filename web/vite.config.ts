// KIKO web configuration; see the root NOTICE.
import { fileURLToPath, pathToFileURL } from 'node:url';
import { defineConfig, type PluginOption } from 'vite';
import vue from '@vitejs/plugin-vue';
import { d2MockServerPlugin } from './src/direction2/mock-server/vite-plugin';

const useRealBackend = process.env['DENTITY_USE_REAL_BACKEND'] === 'true';
const webLicensesPath = fileURLToPath(new URL('./scripts/web-licenses.mjs', import.meta.url));

async function loadWebLicensesPlugin(): Promise<PluginOption[]> {
  const mod = (await import(pathToFileURL(webLicensesPath).href)) as {
    webLicenses?: (opts: { repositoryRoot: string }) => PluginOption;
  };
  return mod.webLicenses
    ? [mod.webLicenses({ repositoryRoot: fileURLToPath(new URL('.', import.meta.url)) })]
    : [];
}

export default defineConfig(async ({ command, mode }) => ({
  define: {
    'import.meta.env.VITE_PLATFORM_REAL': JSON.stringify(
      useRealBackend || command === 'build' || mode === 'production' || process.env['NODE_ENV'] === 'production'
    ),
  },
  plugins: [
    vue(),
    ...(await loadWebLicensesPlugin()),
    ...(!useRealBackend ? [d2MockServerPlugin()] : []),
  ],
  server: {
    host: '127.0.0.1', port: 15173, strictPort: true,
    proxy: { '/api': { target: process.env['DENTITY_CONTROL_PLANE_URL'] ?? 'http://127.0.0.1:18300', changeOrigin: false } },
  },
}));
