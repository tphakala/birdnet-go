/**
 * Global Setup for Reverse Proxy Integration Tests
 *
 * Starts a BirdNET-Go backend and an nginx Docker container configured as a
 * reverse proxy. Tests verify that all valid routes are accessible through
 * the proxy without returning 404 errors.
 *
 * Supports two proxy configurations:
 * - Root proxy: nginx at / → backend
 * - Subpath proxy: nginx at /birdnet/ → backend (with X-Forwarded-Prefix)
 *
 * Note: This runs in Node.js, not in the browser.
 */

/* eslint-disable no-console -- Console output is intentional for test setup feedback */
/* eslint-disable no-undef -- This file runs in Node.js where process/Buffer are globals */

import { type ChildProcess, execSync, spawn } from 'child_process';
import { readFileSync, mkdtempSync, writeFileSync, rmSync } from 'fs';
import { dirname, resolve, join } from 'path';
import { tmpdir } from 'os';
import { fileURLToPath } from 'url';

const BACKEND_STARTUP_TIMEOUT = 60000;
const NGINX_STARTUP_TIMEOUT = 30000;
const HEALTH_CHECK_INTERVAL = 1000;
const BACKEND_PORT = 8080;
const NGINX_ROOT_PORT = 8180;
const NGINX_SUBPATH_PORT = 8181;
const NGINX_IMAGE = 'nginx:1.27-alpine';

let backendProcess: ChildProcess | null = null;
let rootProxyContainerId: string | null = null;
let subpathProxyContainerId: string | null = null;
let tmpDir: string | null = null;

/**
 * Wait for an HTTP endpoint to respond with a non-error status.
 */
async function waitForHTTP(url: string, timeoutMs: number): Promise<boolean> {
  const startTime = Date.now();
  while (Date.now() - startTime < timeoutMs) {
    try {
      const response = await fetch(url, {
        signal: AbortSignal.timeout(5000),
      });
      if (response.ok || response.status < 500) {
        return true;
      }
    } catch {
      // Not ready yet
    }
    await new Promise(r => setTimeout(r, HEALTH_CHECK_INTERVAL));
  }
  return false;
}

/**
 * Check if backend is already running.
 */
async function isBackendRunning(): Promise<boolean> {
  try {
    const response = await fetch(`http://localhost:${BACKEND_PORT}/api/v2/health`, {
      signal: AbortSignal.timeout(3000),
    });
    return response.ok;
  } catch {
    return false;
  }
}

/**
 * Docker host address for container-to-host communication.
 * host.docker.internal works on Mac/Windows natively, and on Linux
 * via the --add-host=host.docker.internal:host-gateway flag (Docker 20.10+).
 */
const DOCKER_HOST = 'host.docker.internal';

/**
 * Start an nginx container with the given config.
 */
function startNginxContainer(
  configPath: string,
  hostPort: number,
  name: string,
  backendHost: string
): string {
  // Read and template the config
  let config = readFileSync(configPath, 'utf-8');
  config = config.replace(/BACKEND_HOST/g, backendHost);
  config = config.replace(/BACKEND_PORT/g, String(BACKEND_PORT));

  // Write templated config to temp dir
  if (!tmpDir) {
    throw new Error('tmpDir not initialized');
  }
  const templatedPath = join(tmpDir, `${name}.conf`);
  writeFileSync(templatedPath, config);

  // Remove stale container from a previous crashed run (ignore errors if absent)
  try {
    execSync(`docker rm -f birdnet-test-${name}`, { stdio: 'ignore', timeout: 10000 });
  } catch {
    // No stale container — expected
  }

  // Start nginx container
  const containerId = execSync(
    [
      'docker run -d --rm',
      `--name birdnet-test-${name}`,
      `-p ${hostPort}:80`,
      `-v ${templatedPath}:/etc/nginx/nginx.conf:ro`,
      // Add extra hosts for Linux Docker host resolution
      '--add-host=host.docker.internal:host-gateway',
      NGINX_IMAGE,
    ].join(' '),
    { encoding: 'utf-8', timeout: 30000 }
  ).trim();

  console.log(`  Started nginx container ${name}: ${containerId.substring(0, 12)}`);
  return containerId;
}

/**
 * Stop and remove a Docker container.
 */
function stopContainer(containerId: string): void {
  try {
    execSync(`docker stop ${containerId}`, { timeout: 10000, stdio: 'ignore' });
  } catch {
    // Container may already be stopped
  }
}

export async function setup(): Promise<void> {
  console.log('\n🔧 Reverse Proxy Integration Test Setup');

  // Get paths
  const currentDir = dirname(fileURLToPath(import.meta.url));
  const projectRoot = resolve(currentDir, '../../..');
  const nginxConfigDir = resolve(currentDir, 'nginx');

  // Check if Docker is available (before allocating temp resources)
  try {
    execSync('docker info', { stdio: 'ignore', timeout: 5000 });
  } catch {
    throw new Error('Docker is not available. Reverse proxy tests require Docker.');
  }

  // Create temp dir for templated configs
  tmpDir = mkdtempSync(join(tmpdir(), 'birdnet-nginx-test-'));

  // Pull nginx image if not present
  console.log('📦 Ensuring nginx image is available...');
  try {
    execSync(`docker pull ${NGINX_IMAGE}`, { stdio: 'ignore', timeout: 60000 });
  } catch {
    console.warn('⚠️ Could not pull nginx image, using cached version');
  }

  // Step 1: Start backend (or use existing)
  if (await isBackendRunning()) {
    console.log('✅ Backend already running at port', BACKEND_PORT);
  } else {
    console.log('🚀 Starting backend with air...');

    backendProcess = spawn('air', ['realtime'], {
      cwd: projectRoot,
      stdio: ['ignore', 'pipe', 'pipe'],
      detached: true,
    });

    backendProcess.stdout?.on('data', (data: Buffer) => {
      const output = data.toString().trim();
      if (output) console.log('[backend]', output);
    });

    backendProcess.stderr?.on('data', (data: Buffer) => {
      const output = data.toString().trim();
      if (output) console.error('[backend:err]', output);
    });

    backendProcess.on('error', (err: Error) => {
      console.error('❌ Failed to start backend:', err.message);
    });

    backendProcess.on('exit', (code: number | null) => {
      if (code !== null && code !== 0) {
        console.warn('⚠️ Backend exited with code:', code);
      }
      backendProcess = null;
    });

    console.log('⏳ Waiting for backend...');
    const backendReady = await waitForHTTP(
      `http://localhost:${BACKEND_PORT}/api/v2/health`,
      BACKEND_STARTUP_TIMEOUT
    );

    if (!backendReady) {
      await teardown();
      throw new Error('Backend failed to start');
    }
    console.log('✅ Backend is ready');
  }

  // Step 2: Docker host address
  console.log(`🐳 Docker host: ${DOCKER_HOST}`);

  // Step 3: Start nginx containers
  console.log('🚀 Starting nginx reverse proxy containers...');

  try {
    rootProxyContainerId = startNginxContainer(
      join(nginxConfigDir, 'root-proxy.conf'),
      NGINX_ROOT_PORT,
      'root-proxy',
      DOCKER_HOST
    );

    subpathProxyContainerId = startNginxContainer(
      join(nginxConfigDir, 'subpath-proxy.conf'),
      NGINX_SUBPATH_PORT,
      'subpath-proxy',
      DOCKER_HOST
    );
  } catch (err) {
    console.error('❌ Failed to start nginx containers:', err);
    await teardown();
    throw err;
  }

  // Step 4: Wait for nginx to be ready
  console.log('⏳ Waiting for nginx proxies...');

  const rootReady = await waitForHTTP(
    `http://localhost:${NGINX_ROOT_PORT}/api/v2/health`,
    NGINX_STARTUP_TIMEOUT
  );
  if (!rootReady) {
    // Show nginx logs for debugging
    try {
      const logs = execSync(`docker logs birdnet-test-root-proxy 2>&1`, {
        encoding: 'utf-8',
        timeout: 5000,
      });
      console.error('nginx root-proxy logs:', logs);
    } catch {
      // ignore
    }
    await teardown();
    throw new Error('Root proxy nginx failed to start');
  }
  console.log('  ✅ Root proxy ready at http://localhost:' + NGINX_ROOT_PORT);

  const subpathReady = await waitForHTTP(
    `http://localhost:${NGINX_SUBPATH_PORT}/birdnet/api/v2/health`,
    NGINX_STARTUP_TIMEOUT
  );
  if (!subpathReady) {
    try {
      const logs = execSync(`docker logs birdnet-test-subpath-proxy 2>&1`, {
        encoding: 'utf-8',
        timeout: 5000,
      });
      console.error('nginx subpath-proxy logs:', logs);
    } catch {
      // ignore
    }
    await teardown();
    throw new Error('Subpath proxy nginx failed to start');
  }
  console.log('  ✅ Subpath proxy ready at http://localhost:' + NGINX_SUBPATH_PORT + '/birdnet/');

  // Export URLs for tests via environment variables (read by test setup)
  process.env.NGINX_ROOT_URL = `http://localhost:${NGINX_ROOT_PORT}`;
  process.env.NGINX_SUBPATH_URL = `http://localhost:${NGINX_SUBPATH_PORT}/birdnet`;
  process.env.BACKEND_URL = `http://localhost:${BACKEND_PORT}`;

  console.log('✅ Reverse proxy test environment ready\n');
}

export async function teardown(): Promise<void> {
  console.log('\n🛑 Tearing down reverse proxy test environment...');

  // Stop nginx containers
  if (rootProxyContainerId) {
    console.log('  Stopping root proxy container...');
    stopContainer(rootProxyContainerId);
    rootProxyContainerId = null;
  }

  if (subpathProxyContainerId) {
    console.log('  Stopping subpath proxy container...');
    stopContainer(subpathProxyContainerId);
    subpathProxyContainerId = null;
  }

  // Stop backend
  if (backendProcess !== null) {
    console.log('  Stopping backend...');
    const pid = backendProcess.pid;
    try {
      if (pid !== undefined) {
        process.kill(-pid, 'SIGTERM');
      }
    } catch {
      // Process may have already exited
    }
    await new Promise(r => setTimeout(r, 1000));
    // Force kill if still running — use captured pid since backendProcess
    // may have been set to null by the 'exit' handler during the await
    try {
      if (pid !== undefined) {
        process.kill(-pid, 'SIGKILL');
      }
    } catch {
      // Already dead
    }
    backendProcess = null;
  }

  // Clean up temp dir
  if (tmpDir) {
    try {
      rmSync(tmpDir, { recursive: true, force: true });
    } catch {
      // Ignore cleanup errors
    }
    tmpDir = null;
  }

  console.log('✅ Teardown complete');
}
