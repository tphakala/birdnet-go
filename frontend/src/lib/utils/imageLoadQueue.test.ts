import { describe, it, expect, beforeEach } from 'vitest';
import {
  acquireSlot,
  releaseSlot,
  getQueueStats,
  resetQueue,
  MAX_CONCURRENT_IMAGE_LOADS,
} from './imageLoadQueue';

describe('imageLoadQueue', () => {
  beforeEach(() => {
    resetQueue();
  });

  describe('MAX_CONCURRENT_IMAGE_LOADS', () => {
    it('is set to 2', () => {
      expect(MAX_CONCURRENT_IMAGE_LOADS).toBe(2);
    });
  });

  describe('acquireSlot', () => {
    it('resolves immediately when slots available', async () => {
      const handle = acquireSlot();
      const result = await handle.promise;
      expect(result).toBe(true);
    });

    it('allows up to MAX_CONCURRENT_IMAGE_LOADS concurrent acquisitions', async () => {
      const handles = [];
      for (let i = 0; i < MAX_CONCURRENT_IMAGE_LOADS; i++) {
        const handle = acquireSlot();
        handles.push(handle);
        const result = await handle.promise;
        expect(result).toBe(true);
      }
      expect(getQueueStats().active).toBe(MAX_CONCURRENT_IMAGE_LOADS);
    });

    it('queues requests beyond the limit', async () => {
      // Fill all slots
      const handles = [];
      for (let i = 0; i < MAX_CONCURRENT_IMAGE_LOADS; i++) {
        const handle = acquireSlot();
        handles.push(handle);
        await handle.promise;
      }

      // This one should be queued
      const queuedHandle = acquireSlot();
      let resolved = false;
      queuedHandle.promise.then(() => {
        resolved = true;
      });

      // Wait a tick - should still be pending
      await new Promise(r => setTimeout(r, 10));
      expect(resolved).toBe(false);
      expect(getQueueStats().queued).toBe(1);
    });

    it('processes queued requests when slots are released', async () => {
      // Fill all slots
      for (let i = 0; i < MAX_CONCURRENT_IMAGE_LOADS; i++) {
        const handle = acquireSlot();
        await handle.promise;
      }

      // Queue one more
      const queuedHandle = acquireSlot();
      let resolved = false;
      queuedHandle.promise.then(() => {
        resolved = true;
      });

      // Release a slot
      releaseSlot();

      // Wait for queued request to be processed
      await queuedHandle.promise;
      expect(resolved).toBe(true);
    });
  });

  describe('cancel', () => {
    it('resolves with false when cancelled while waiting', async () => {
      // Fill all slots
      for (let i = 0; i < MAX_CONCURRENT_IMAGE_LOADS; i++) {
        const handle = acquireSlot();
        await handle.promise;
      }

      // Queue one more and cancel it
      const queuedHandle = acquireSlot();
      queuedHandle.cancel();

      const result = await queuedHandle.promise;
      expect(result).toBe(false);
      expect(getQueueStats().queued).toBe(0);
    });

    it('cancel is idempotent', async () => {
      // Fill all slots
      for (let i = 0; i < MAX_CONCURRENT_IMAGE_LOADS; i++) {
        const handle = acquireSlot();
        await handle.promise;
      }

      const queuedHandle = acquireSlot();
      queuedHandle.cancel();
      queuedHandle.cancel(); // Should not throw

      const result = await queuedHandle.promise;
      expect(result).toBe(false);
    });
  });

  describe('releaseSlot', () => {
    it('decrements active count', async () => {
      const handle = acquireSlot();
      await handle.promise;
      expect(getQueueStats().active).toBe(1);

      releaseSlot();
      expect(getQueueStats().active).toBe(0);
    });

    it('does not go below zero', () => {
      releaseSlot();
      releaseSlot();
      expect(getQueueStats().active).toBe(0);
    });
  });

  describe('getQueueStats', () => {
    it('returns current queue statistics', async () => {
      const stats = getQueueStats();
      expect(stats).toHaveProperty('active');
      expect(stats).toHaveProperty('queued');
      expect(stats).toHaveProperty('maxConcurrent');
      expect(stats.maxConcurrent).toBe(MAX_CONCURRENT_IMAGE_LOADS);
    });
  });
});
