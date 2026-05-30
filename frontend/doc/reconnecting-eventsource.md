# ReconnectingEventSource Implementation Guide

## Overview

We use a small local `ReconnectingEventSource` class to handle Server-Sent Events
(SSE) with automatic reconnection. It wraps the native browser `EventSource` and
transparently reconnects when the connection drops, so call sites do not have to
hand-roll retry logic.

The class lives at `src/lib/utils/ReconnectingEventSource.ts`. It replaced the
external `reconnecting-eventsource` npm package to remove a runtime dependency.
It is self-typed, so no ambient type declaration file is needed.

## Usage Example

### Basic Implementation

```typescript
import { ReconnectingEventSource } from '$lib/utils/ReconnectingEventSource';

// Create connection with options
const eventSource = new ReconnectingEventSource('/api/endpoint', {
  max_retry_time: 30000, // Upper bound for the reconnect backoff, in ms
  withCredentials: false, // Set to true to send cookies cross-origin
});

// Handle connection open
eventSource.onopen = () => {
  console.log('SSE connection opened');
};

// Handle default (unnamed) messages
eventSource.onmessage = event => {
  try {
    const data = JSON.parse(event.data);
    // Process data
  } catch (error) {
    console.error('Failed to parse SSE message:', error);
  }
};

// Handle named events
eventSource.addEventListener('detection', event => {
  const data = JSON.parse((event as MessageEvent).data);
  // Process data
});

// Handle errors (reconnection is automatic)
eventSource.onerror = error => {
  console.error('SSE error:', error);
  // No manual reconnection needed - handled automatically
};

// Cleanup
eventSource.close();
```

## Public Surface

- `new ReconnectingEventSource(url, options?)` where `url` is a `string | URL`
- Properties: `url`, `withCredentials`, `readyState` (`0` CONNECTING, `1` OPEN, `2` CLOSED)
- Static and instance constants: `CONNECTING`, `OPEN`, `CLOSED`
- Settable handlers: `onopen`, `onmessage`, `onerror`
- Methods: `addEventListener(type, listener)`, `removeEventListener(type, listener)`, `close()`

## Configuration Options

- `max_retry_time`: upper bound for the reconnect backoff delay, in milliseconds (default `3000`)
- `withCredentials`: include cookies in cross-origin requests (default `false`)
- `eventSourceClass`: override the `EventSource` implementation, e.g. a polyfill or a test double (default `globalThis.EventSource`)

## Reconnection Behavior

When an open connection drops and the native source gives up (its `readyState`
reaches `CLOSED`), the wrapper reconnects with full-jitter exponential backoff:
the delay window starts small (faster recovery from a transient blip), doubles on
each consecutive failure, and is capped at `max_retry_time`. The window resets
after a successful (re)connect. At steady state it converges to
`random(0, max_retry_time)`, so it never waits longer than a flat random delay
would.

A connection that is still `CONNECTING` (the native source is retrying on its
own) is left alone; the wrapper only takes over once the native source closes.

Note: the class does not track `lastEventId`. BirdNET-Go's SSE endpoints emit no
`id:` field and the server reads no `lastEventId` parameter, so resumption
tracking would be dead code here.

## Best Practices

1. **Always close connections** when the component unmounts.
2. **Handle parse errors** gracefully in message handlers.
3. **Log connection events** for debugging.
4. **Set an appropriate `max_retry_time`** for the endpoint.
5. **Consider visibility changes** - close the connection when the page is hidden.

## Example Implementation in Svelte 5

See `AudioLevelIndicator.svelte` for a complete implementation example.
