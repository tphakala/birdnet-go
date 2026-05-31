/**
 * ReconnectingEventSource wraps the native browser EventSource and transparently
 * reconnects when the connection drops. It replaces the `reconnecting-eventsource`
 * npm package, removing a runtime dependency.
 *
 * Behaviour mirrors the upstream package's public surface used across the app
 * (`new ReconnectingEventSource(url, { withCredentials, max_retry_time })`,
 * `onopen`/`onmessage`/`onerror`, `addEventListener`/`removeEventListener`,
 * `close()`) with one deliberate improvement: reconnection uses full-jitter
 * exponential backoff capped at `max_retry_time`, rather than a flat random
 * delay. The backoff window starts small (faster recovery from a transient
 * blip), grows on consecutive failures, resets after a successful (re)connect,
 * and at steady state converges to `random(0, max_retry_time)` so it never waits
 * longer than the upstream package would.
 *
 * The upstream package also tracked `lastEventId` and appended it as a query
 * parameter on reconnect. BirdNET-Go's SSE endpoints emit no `id:` field (the
 * server writes only `event:`/`data:`; see internal/api/v2/sse.go) and the server
 * reads no `lastEventId` parameter, so that path was dead and is omitted.
 */

/** Default upper bound for the reconnect backoff delay, in milliseconds. */
const DEFAULT_MAX_RETRY_TIME_MS = 3000;
/** Initial backoff window, in milliseconds, before any exponential growth. */
const INITIAL_RETRY_TIME_MS = 500;
/** Native EventSource.readyState value indicating the source has given up. */
const NATIVE_CLOSED = 2;

export interface ReconnectingEventSourceInit {
  /** Send credentials (cookies) with the SSE request. Defaults to false. */
  withCredentials?: boolean;
  /** Upper bound for the reconnect backoff delay, in milliseconds (default 3000). */
  max_retry_time?: number;
  /**
   * EventSource implementation to instantiate. Defaults to `globalThis.EventSource`.
   * Override to supply a polyfill or a test double.
   */
  eventSourceClass?: typeof EventSource;
}

type EventListenerFn = (event: Event) => void;

export class ReconnectingEventSource {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSED = 2;

  readonly CONNECTING = 0;
  readonly OPEN = 1;
  readonly CLOSED = 2;

  readonly url: string;
  readonly withCredentials: boolean;
  readyState: 0 | 1 | 2 = ReconnectingEventSource.CONNECTING;

  onopen: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;

  private readonly maxRetryTime: number;
  private readonly eventSourceClass: typeof EventSource;
  private readonly init: EventSourceInit;
  private eventSource: EventSource | null = null;
  private readonly listeners = new Map<string, Set<EventListenerFn>>();
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private retryDelay = INITIAL_RETRY_TIME_MS;
  private closed = false;

  // A single bound forwarder so add/removeEventListener on the native source use
  // a stable reference and unsubscribe correctly.
  private readonly forwardEvent = (event: Event): void => this.dispatch(event);

  constructor(url: string | URL, init: ReconnectingEventSourceInit = {}) {
    this.url = url.toString();
    this.withCredentials = init.withCredentials ?? false;
    this.maxRetryTime = init.max_retry_time ?? DEFAULT_MAX_RETRY_TIME_MS;

    const EventSourceImpl = init.eventSourceClass ?? globalThis.EventSource;
    if (typeof EventSourceImpl !== 'function') {
      throw new Error(
        'EventSource is not available in this environment. Provide an eventSourceClass to ReconnectingEventSource.'
      );
    }
    this.eventSourceClass = EventSourceImpl;
    this.init = { withCredentials: this.withCredentials };

    this.connect();
  }

  private connect(): void {
    const source = new this.eventSourceClass(this.url, this.init);
    this.eventSource = source;
    source.onopen = event => this.handleOpen(event);
    source.onerror = event => this.handleError(event);
    source.onmessage = event => this.onmessage?.(event);
    // Re-apply named listeners registered before this (re)connection.
    for (const type of this.listeners.keys()) {
      source.addEventListener(type, this.forwardEvent);
    }
  }

  private handleOpen(event: Event): void {
    // A successful (re)connect resets the backoff window.
    this.retryDelay = INITIAL_RETRY_TIME_MS;
    if (this.readyState === ReconnectingEventSource.CONNECTING) {
      this.readyState = ReconnectingEventSource.OPEN;
      this.onopen?.(event);
    }
  }

  private handleError(event: Event): void {
    if (this.readyState === ReconnectingEventSource.OPEN) {
      this.readyState = ReconnectingEventSource.CONNECTING;
      this.onerror?.(event);
    }
    // Only take over reconnection once the native source has fully closed.
    // A source still in CONNECTING is retrying on its own; leave it be.
    const source = this.eventSource;
    if (source?.readyState === NATIVE_CLOSED) {
      source.close();
      this.eventSource = null;
      this.scheduleReconnect();
    }
  }

  private scheduleReconnect(): void {
    if (this.closed) {
      return;
    }
    // Full-jitter exponential backoff, capped at maxRetryTime.
    const backoffWindow = Math.min(this.retryDelay, this.maxRetryTime);
    const delay = Math.round(Math.random() * backoffWindow);
    this.retryDelay = Math.min(this.retryDelay * 2, this.maxRetryTime);
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      if (!this.closed) {
        this.connect();
      }
    }, delay);
  }

  private dispatch(event: Event): void {
    const listenersForType = this.listeners.get(event.type);
    if (!listenersForType) {
      return;
    }
    // Iterate a copy so a listener removing itself does not disturb the loop.
    for (const listener of [...listenersForType]) {
      listener.call(this, event);
    }
  }

  addEventListener(type: string, listener: EventListenerFn): void {
    let listenersForType = this.listeners.get(type);
    if (!listenersForType) {
      listenersForType = new Set();
      this.listeners.set(type, listenersForType);
      this.eventSource?.addEventListener(type, this.forwardEvent);
    }
    listenersForType.add(listener);
  }

  removeEventListener(type: string, listener: EventListenerFn): void {
    const listenersForType = this.listeners.get(type);
    if (!listenersForType) {
      return;
    }
    listenersForType.delete(listener);
    if (listenersForType.size === 0) {
      this.listeners.delete(type);
      this.eventSource?.removeEventListener(type, this.forwardEvent);
    }
  }

  close(): void {
    this.closed = true;
    if (this.reconnectTimer !== null) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    if (this.eventSource) {
      this.eventSource.close();
      this.eventSource = null;
    }
    // Drop references to caller-provided listener closures promptly.
    this.listeners.clear();
    this.readyState = ReconnectingEventSource.CLOSED;
  }
}
