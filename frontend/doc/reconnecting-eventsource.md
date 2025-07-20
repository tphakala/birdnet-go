# ReconnectingEventSource Implementation Guide

## Overview

We use the `reconnecting-eventsource` package to handle Server-Sent Events (SSE) with automatic reconnection capabilities. This replaces manual reconnection logic with a more robust solution.

## Installation

```bash
npm install reconnecting-eventsource
```

## Usage Example

### Basic Implementation

```typescript
import ReconnectingEventSource from 'reconnecting-eventsource';

// Create connection with options
const eventSource = new ReconnectingEventSource('/api/endpoint', {
  max_retry_time: 30000, // Max 30 seconds between reconnection attempts
  withCredentials: false, // Set to true if you need CORS credentials
});

// Handle connection open
eventSource.onopen = () => {
  console.log('SSE connection opened');
};

// Handle messages
eventSource.onmessage = event => {
  try {
    const data = JSON.parse(event.data);
    // Process data
  } catch (error) {
    console.error('Failed to parse SSE message:', error);
  }
};

// Handle errors (reconnection is automatic)
eventSource.onerror = error => {
  console.error('SSE error:', error);
  // No manual reconnection needed - handled automatically
};

// Cleanup
eventSource.close();
```

### TypeScript Support

Since the package doesn't include TypeScript definitions, create a type declaration file:

```typescript
// src/lib/types/reconnecting-eventsource.d.ts
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
```

## Benefits

1. **Automatic Reconnection**: No need for manual reconnection logic
2. **Exponential Backoff**: Built-in intelligent retry delays
3. **Drop-in Replacement**: Works like standard EventSource
4. **Configurable**: Customize retry behavior and credentials

## Configuration Options

- `max_retry_time`: Maximum time in milliseconds to wait between reconnection attempts (default: 3000)
- `withCredentials`: Include cookies in CORS requests (default: false)
- `eventSourceClass`: Override the EventSource implementation (default: window.EventSource)

## Migration from Manual Reconnection

### Before (Manual Reconnection)

```typescript
let eventSource: EventSource | null = null;
let reconnectAttempts = 0;
let reconnectDelay = 1000;

function setupEventSource() {
  eventSource = new EventSource('/api/endpoint');

  eventSource.onerror = () => {
    if (!isNavigating && reconnectAttempts < 10) {
      setTimeout(() => {
        reconnectAttempts++;
        reconnectDelay = Math.min(reconnectDelay * 1.5, 15000);
        setupEventSource();
      }, reconnectDelay);
    }
  };
}
```

### After (ReconnectingEventSource)

```typescript
import ReconnectingEventSource from 'reconnecting-eventsource';

let eventSource: ReconnectingEventSource | null = null;

function setupEventSource() {
  eventSource = new ReconnectingEventSource('/api/endpoint', {
    max_retry_time: 30000,
  });

  eventSource.onerror = error => {
    console.error('SSE error:', error);
    // Reconnection handled automatically
  };
}
```

## Best Practices

1. **Always close connections** when component unmounts
2. **Handle parse errors** gracefully in message handlers
3. **Log connection events** for debugging
4. **Set appropriate max_retry_time** based on your use case
5. **Consider visibility changes** - close connection when page is hidden

## Example Implementation in Svelte 5

See `AudioLevelIndicator.svelte` for a complete implementation example.
