// Type declarations for reconnecting-eventsource

declare module 'reconnecting-eventsource' {
  interface ReconnectingEventSourceOptions {
    withCredentials?: boolean;
    max_retry_time?: number;
    eventSourceClass?: typeof EventSource;
  }

  class ReconnectingEventSource extends EventTarget {
    constructor(url: string, options?: ReconnectingEventSourceOptions);

    readonly url: string;
    readonly readyState: number;
    readonly withCredentials: boolean;

    onopen: ((event: Event) => void) | null;
    onmessage: ((event: MessageEvent) => void) | null;
    onerror: ((event: Event) => void) | null;

    close(): void;
    addEventListener(
      type: string,
      listener: EventListenerOrEventListenerObject,
      options?: boolean | AddEventListenerOptions
    ): void;
    removeEventListener(
      type: string,
      listener: EventListenerOrEventListenerObject,
      options?: boolean | EventListenerOptions
    ): void;

    static readonly CONNECTING: 0;
    static readonly OPEN: 1;
    static readonly CLOSED: 2;
  }

  export = ReconnectingEventSource;
}
