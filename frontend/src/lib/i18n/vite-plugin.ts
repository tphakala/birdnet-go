import { readdir } from 'fs/promises';
import type { Plugin } from 'vite';

interface I18nPluginOptions {
  messagesDir?: string;
}

/**
 * Vite plugin for auto-discovering i18n languages and enabling HMR
 */
export function i18nAutoDiscovery(options: I18nPluginOptions = {}): Plugin {
  const { messagesDir = './static/messages' } = options;

  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  let server: any;

  async function logDiscoveredLocales() {
    try {
      // Read messages directory to discover locales
      // Safe path - comes from configuration
      // eslint-disable-next-line security/detect-non-literal-fs-filename
      const files = await readdir(messagesDir);
      const locales = files
        .filter(file => file.endsWith('.json'))
        .map(file => file.replace('.json', ''))
        .sort();

      // eslint-disable-next-line no-console
      console.log(`[i18n-auto-discovery] Found locales: ${locales.join(', ')}`);
    } catch (error) {
      // eslint-disable-next-line no-console
      console.error('[i18n-auto-discovery] Error reading messages directory:', error);
    }
  }

  return {
    name: 'i18n-auto-discovery',
    configureServer(devServer) {
      server = devServer;

      // Watch messages directory
      devServer.watcher.add(messagesDir);

      // Listen for file changes
      devServer.watcher.on('add', filePath => {
        if (filePath.includes(messagesDir) && filePath.endsWith('.json')) {
          // eslint-disable-next-line no-console
          console.log(`[i18n-auto-discovery] New locale file added: ${filePath}`);
          void logDiscoveredLocales();
          // Trigger HMR
          if (server) {
            server.ws.send({
              type: 'full-reload',
            });
          }
        }
      });

      devServer.watcher.on('unlink', filePath => {
        if (filePath.includes(messagesDir) && filePath.endsWith('.json')) {
          // eslint-disable-next-line no-console
          console.log(`[i18n-auto-discovery] Locale file removed: ${filePath}`);
          void logDiscoveredLocales();
          // Trigger HMR
          if (server) {
            server.ws.send({
              type: 'full-reload',
            });
          }
        }
      });

      devServer.watcher.on('change', filePath => {
        if (filePath.includes(messagesDir) && filePath.endsWith('.json')) {
          // eslint-disable-next-line no-console
          console.log(`[i18n-auto-discovery] Locale file changed: ${filePath}`);
          // Trigger HMR for message changes
          if (server) {
            server.ws.send({
              type: 'full-reload',
            });
          }
        }
      });
    },
    buildStart() {
      // Log discovered locales at build start
      void logDiscoveredLocales();
    },
  };
}
