/**
 * Global Setup for Integration Tests with Automatic Backend Management
 *
 * This setup can automatically start and stop the backend for integration tests.
 * It's used by the `test:integration:auto` script.
 *
 * Note: This runs in Node.js, not in the browser.
 * The browser tests will connect to the backend through Vite's proxy.
 */

/* eslint-disable no-console -- Console output is intentional for test setup feedback */
/* eslint-disable no-undef -- This file runs in Node.js where process/Buffer are globals */

import { type ChildProcess, spawn } from 'child_process';
import { dirname, resolve } from 'path';
import { fileURLToPath } from 'url';

const BACKEND_STARTUP_TIMEOUT = 60000; // 60 seconds
const HEALTH_CHECK_INTERVAL = 1000; // 1 second
const BACKEND_URL = 'http://localhost:8080';

let backendProcess: ChildProcess | null = null;

/**
 * Wait for backend health endpoint to respond
 */
async function waitForBackend(timeoutMs: number): Promise<boolean> {
  const startTime = Date.now();

  while (Date.now() - startTime < timeoutMs) {
    try {
      const response = await fetch(`${BACKEND_URL}/api/v2/health`, {
        signal: AbortSignal.timeout(5000),
      });
      if (response.ok) {
        return true;
      }
    } catch {
      // Backend not ready yet
    }
    await new Promise(r => setTimeout(r, HEALTH_CHECK_INTERVAL));
  }

  return false;
}

/**
 * Check if backend is already running
 */
async function isBackendRunning(): Promise<boolean> {
  try {
    const response = await fetch(`${BACKEND_URL}/api/v2/health`, {
      signal: AbortSignal.timeout(3000),
    });
    return response.ok;
  } catch {
    return false;
  }
}

export async function setup(): Promise<void> {
  console.log('\n🔧 Integration Test Global Setup');

  // Check if backend is already running
  if (await isBackendRunning()) {
    console.log('✅ Backend already running at', BACKEND_URL);
    return;
  }

  console.log('🚀 Starting backend with air...');

  // Get project root from current file location
  const currentDir = dirname(fileURLToPath(import.meta.url));
  const projectRoot = resolve(currentDir, '../../..');

  backendProcess = spawn('air', ['realtime'], {
    cwd: projectRoot,
    stdio: ['ignore', 'pipe', 'pipe'],
    detached: true,
  });

  // Log backend output
  backendProcess.stdout?.on('data', (data: Buffer) => {
    const output = data.toString().trim();
    if (output) {
      console.log('[backend]', output);
    }
  });

  backendProcess.stderr?.on('data', (data: Buffer) => {
    const output = data.toString().trim();
    if (output) {
      console.error('[backend:err]', output);
    }
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

  // Wait for backend to be ready
  console.log('⏳ Waiting for backend to be ready...');
  const ready = await waitForBackend(BACKEND_STARTUP_TIMEOUT);

  if (ready) {
    console.log('✅ Backend is ready');
  } else {
    console.error('❌ Backend failed to start within', BACKEND_STARTUP_TIMEOUT / 1000, 'seconds');
    await teardown();
    throw new Error('Backend startup timeout');
  }
}

export async function teardown(): Promise<void> {
  if (backendProcess === null) {
    return;
  }

  console.log('\n🛑 Stopping backend...');

  const proc = backendProcess;

  // Kill the backend process group
  try {
    if (proc.pid !== undefined) {
      // Kill process group (negative PID)
      process.kill(-proc.pid, 'SIGTERM');
    }
  } catch {
    // Process may have already exited
  }

  // Wait a moment for graceful shutdown
  await new Promise(r => setTimeout(r, 1000));

  // Force kill if still running
  if (!proc.killed) {
    try {
      if (proc.pid !== undefined) {
        process.kill(-proc.pid, 'SIGKILL');
      }
    } catch {
      // Process already dead
    }
  }

  backendProcess = null;
  console.log('✅ Backend stopped');
}
