import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { createDebounce } from './debounce';

describe('createDebounce', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('delays execution by the specified duration', () => {
    const fn = vi.fn();
    const debounced = createDebounce(fn, 100);

    debounced();
    expect(fn).not.toHaveBeenCalled();

    vi.advanceTimersByTime(99);
    expect(fn).not.toHaveBeenCalled();

    vi.advanceTimersByTime(1);
    expect(fn).toHaveBeenCalledOnce();
  });

  it('coalesces rapid calls into one execution', () => {
    const fn = vi.fn();
    const debounced = createDebounce(fn, 100);

    debounced();
    debounced();
    debounced();

    vi.advanceTimersByTime(100);
    expect(fn).toHaveBeenCalledOnce();
  });

  it('forwards arguments from the last call', () => {
    const fn = vi.fn<(a: string, b: number) => void>();
    const debounced = createDebounce(fn, 100);

    debounced('first', 1);
    debounced('second', 2);
    debounced('third', 3);

    vi.advanceTimersByTime(100);
    expect(fn).toHaveBeenCalledWith('third', 3);
  });

  it('cancel prevents pending execution', () => {
    const fn = vi.fn();
    const debounced = createDebounce(fn, 100);

    debounced();
    debounced.cancel();

    vi.advanceTimersByTime(200);
    expect(fn).not.toHaveBeenCalled();
  });

  it('flush executes immediately and clears timer', () => {
    const fn = vi.fn();
    const debounced = createDebounce(fn, 100);

    debounced();
    debounced.flush();

    expect(fn).toHaveBeenCalledOnce();

    vi.advanceTimersByTime(200);
    expect(fn).toHaveBeenCalledOnce();
  });

  it('flush is a no-op when nothing is pending', () => {
    const fn = vi.fn();
    const debounced = createDebounce(fn, 100);

    debounced.flush();
    expect(fn).not.toHaveBeenCalled();
  });

  it('resets timer on each call', () => {
    const fn = vi.fn();
    const debounced = createDebounce(fn, 100);

    debounced();
    vi.advanceTimersByTime(80);
    debounced();
    vi.advanceTimersByTime(80);

    expect(fn).not.toHaveBeenCalled();

    vi.advanceTimersByTime(20);
    expect(fn).toHaveBeenCalledOnce();
  });
});
