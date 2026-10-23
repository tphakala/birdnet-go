/** A promise with its resolve and reject exposed, for tests that control timing. */
export interface Deferred<T = void> {
  promise: Promise<T>;
  resolve: (value: T) => void;
  reject: (err: Error) => void;
}

/** Creates a Deferred. The target is ES2022, so Promise.withResolvers is unavailable. */
export function deferred<T = void>(): Deferred<T> {
  let resolve: (value: T) => void = () => {};
  let reject: (err: Error) => void = () => {};
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}
