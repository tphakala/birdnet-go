/** Create a debounced function that delays invocation until `delayMs` after the last call. */
export function createDebounce<T extends unknown[]>(
  fn: (...args: T) => void,
  delayMs: number
): ((...args: T) => void) & { cancel: () => void; flush: () => void } {
  let timer: ReturnType<typeof setTimeout> | undefined;
  let lastArgs: T | undefined;

  function debounced(...args: T): void {
    lastArgs = args;
    if (timer !== undefined) clearTimeout(timer);
    timer = setTimeout(() => {
      timer = undefined;
      const a = lastArgs;
      lastArgs = undefined;
      if (a) fn(...a);
    }, delayMs);
  }

  debounced.cancel = (): void => {
    if (timer !== undefined) clearTimeout(timer);
    timer = undefined;
    lastArgs = undefined;
  };

  debounced.flush = (): void => {
    if (timer === undefined) return;
    clearTimeout(timer);
    timer = undefined;
    const a = lastArgs;
    lastArgs = undefined;
    if (a) fn(...a);
  };

  return debounced;
}
