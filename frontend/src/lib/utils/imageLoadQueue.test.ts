import { describe, it, expect, beforeEach } from 'vitest';
import {
  acquireSlot,
  releaseSlot,
  getQueueStats,
  resetQueue,
  MAX_CONCURRENT_IMAGE_LOADS,
  type SlotHandle,
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
      const handles: SlotHandle[] = [];
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
      const handles: SlotHandle[] = [];
      for (let i = 0; i < MAX_CONCURRENT_IMAGE_LOADS; i++) {
        const handle = acquireSlot();
        handles.push(handle);
        await handle.promise;
      }

      // This one should be queued
      const queuedHandle = acquireSlot();
      let resolved = false;
      void queuedHandle.promise.then(() => {
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
      void queuedHandle.promise.then(() => {
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

    it('cancel is safe to call on immediately acquired slots', async () => {
      const handle = acquireSlot();
      const result = await handle.promise;
      expect(result).toBe(true);

      // Should not throw or have side effects
      handle.cancel();
      expect(getQueueStats().active).toBe(1); // Still active
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

    it('skips cancelled requests when releasing and decrements active count', async () => {
      // Fill all slots
      for (let i = 0; i < MAX_CONCURRENT_IMAGE_LOADS; i++) {
        await acquireSlot().promise;
      }
      expect(getQueueStats().active).toBe(MAX_CONCURRENT_IMAGE_LOADS);

      // Queue a request and cancel it
      const handle = acquireSlot();
      handle.cancel();
      await handle.promise;
      expect(getQueueStats().queued).toBe(0);

      // Release should skip the cancelled request and decrement active count
      releaseSlot();
      expect(getQueueStats().active).toBe(MAX_CONCURRENT_IMAGE_LOADS - 1);
    });

    it('transfers slot to next non-cancelled queued request', async () => {
      // Fill all slots
      for (let i = 0; i < MAX_CONCURRENT_IMAGE_LOADS; i++) {
        await acquireSlot().promise;
      }

      // Queue two requests, cancel the first
      const handle1 = acquireSlot();
      const handle2 = acquireSlot();
      handle1.cancel();

      expect(getQueueStats().queued).toBe(1); // Only handle2 remains

      // Release should skip cancelled handle1 and give slot to handle2
      releaseSlot();
      const result = await handle2.promise;
      expect(result).toBe(true);
      expect(getQueueStats().active).toBe(MAX_CONCURRENT_IMAGE_LOADS);
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

    it('maintains accurate queue stats through complex operation sequences', async () => {
      // Fill slots
      for (let i = 0; i < MAX_CONCURRENT_IMAGE_LOADS; i++) {
        await acquireSlot().promise;
      }
      expect(getQueueStats()).toEqual({
        active: MAX_CONCURRENT_IMAGE_LOADS,
        queued: 0,
        maxConcurrent: MAX_CONCURRENT_IMAGE_LOADS,
      });

      // Queue some requests
      const handles = [acquireSlot(), acquireSlot(), acquireSlot()];
      expect(getQueueStats().queued).toBe(3);

      // Cancel middle one
      handles[1].cancel();
      expect(getQueueStats().queued).toBe(2);

      // Release a slot - should transfer to first non-cancelled
      releaseSlot();
      await handles[0].promise;
      expect(getQueueStats().queued).toBe(1);
      expect(getQueueStats().active).toBe(MAX_CONCURRENT_IMAGE_LOADS);
    });
  });
});
