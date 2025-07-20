import { toastActions } from './toast.js';
import type { ToastPosition, ToastType } from './toast.js';
import ReconnectingEventSource from 'reconnecting-eventsource';

interface SSEToastData {
	message: string;
	type: ToastType;
	duration?: number;
	eventType: string;
	timestamp: string;
}

// Create SSE listener for toast messages
let eventSource: ReconnectingEventSource | null = null;

export function initSSEToasts() {
	if (eventSource) {
		return;
	}

	const sseUrl = '/api/v2/toasts/stream';

	try {
		// Create connection to SSE endpoint
		eventSource = new ReconnectingEventSource(sseUrl, {
			max_retry_time: 30000,
			withCredentials: false,
		});

		eventSource.onopen = () => {
			// Connection opened
		};

		eventSource.onmessage = (event: MessageEvent) => {
			try {
				JSON.parse(event.data);
			} catch {
				// Ignore parsing errors for general messages
			}
		};

		eventSource.onerror = () => {
			// ReconnectingEventSource handles reconnection automatically
		};

		// Handle specific event types
		eventSource.addEventListener('connected', (event: Event) => {
			try {
				const messageEvent = event as MessageEvent;
				JSON.parse(messageEvent.data);
			} catch {
				// Ignore parsing errors for connection events
			}
		});

		// Handle toast messages
		eventSource.addEventListener('toast', (event: Event) => {
			try {
				const messageEvent = event as MessageEvent;
				const toastData: SSEToastData = JSON.parse(messageEvent.data);

				// Show the toast using the toast store
				toastActions.show(toastData.message, toastData.type, {
					duration: toastData.duration ?? 5000,
					position: 'top-right' as ToastPosition
				});
			} catch {
				// Ignore parsing errors for toast data
			}
		});

		// Handle heartbeat
		eventSource.addEventListener('heartbeat', (event: Event) => {
			try {
				const messageEvent = event as MessageEvent;
				JSON.parse(messageEvent.data);
			} catch {
				// Ignore parsing errors for heartbeat
			}
		});

	} catch {
		// Try again in 5 seconds
		setTimeout(() => initSSEToasts(), 5000);
	}
}

export function closeSSEToasts() {
	if (eventSource) {
		eventSource.close();
		eventSource = null;
	}
}

// Auto-initialize when the module is imported
if (typeof window !== 'undefined') {
	// Initialize after a short delay to ensure the app is ready
	setTimeout(initSSEToasts, 100);

	// Cleanup on page unload
	window.addEventListener('beforeunload', closeSSEToasts);
}