<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import ReconnectingEventSource from 'reconnecting-eventsource';
  import { cn } from '$lib/utils/cn';

  interface Notification {
    id: string;
    type: 'error' | 'warning' | 'info' | 'detection' | 'system';
    title: string;
    message: string;
    timestamp: string;
    read: boolean;
    priority: 'critical' | 'high' | 'medium' | 'low';
    component?: string;
  }

  interface SSEMessage {
    eventType: 'connected' | 'notification' | 'heartbeat';
    clientId?: string;
    [key: string]: any;
  }

  interface Props {
    className?: string;
    debugMode?: boolean;
    onNavigateToNotifications?: () => void;
  }

  let { className = '', debugMode = false, onNavigateToNotifications }: Props = $props();

  // State
  let notifications = $state<Notification[]>([]);
  let unreadCount = $state(0);
  let dropdownOpen = $state(false);
  let loading = $state(true);
  let hasUnread = $state(false);
  let soundEnabled = $state(false);

  // Internal state
  let sseConnection: ReconnectingEventSource | null = null;
  let animationTimeout: ReturnType<typeof globalThis.setTimeout> | null = null;
  let dropdownRef = $state<HTMLDivElement>();
  let buttonRef = $state<HTMLButtonElement>();

  // Computed
  const visibleNotifications = $derived(notifications.filter(n => shouldShowNotification(n)));

  // Check if notification should be shown based on debug mode
  function shouldShowNotification(notification: Notification): boolean {
    // Always show user-facing notifications
    if (
      notification.type === 'detection' ||
      notification.priority === 'critical' ||
      notification.priority === 'high'
    ) {
      return true;
    }

    // In debug mode, show all notifications
    if (debugMode) {
      return true;
    }

    // Filter out system/error notifications when not in debug mode
    if (
      notification.type === 'error' ||
      notification.type === 'system' ||
      notification.type === 'warning'
    ) {
      return false;
    }

    return true;
  }

  // Load notifications from API
  async function loadNotifications() {
    loading = true;
    try {
      const response = await fetch('/api/v2/notifications?limit=20&status=unread');
      if (response.ok) {
        const data = await response.json();
        notifications = (data.notifications || []).filter((n: Notification) => shouldShowNotification(n));
        updateUnreadCount();
      }
    } catch {
      // Failed to load notifications
    } finally {
      loading = false;
    }
  }

  // Connect to SSE for real-time notifications using ReconnectingEventSource
  function connectSSE() {
    if (sseConnection) {
      sseConnection.close();
      sseConnection = null;
    }

    try {
      // ReconnectingEventSource with configuration
      sseConnection = new ReconnectingEventSource('/api/v2/notifications/stream', {
        max_retry_time: 30000, // Max 30 seconds between reconnection attempts
        withCredentials: false
      });

      sseConnection.onopen = () => {
        console.log('Notification SSE connection opened');
      };

      sseConnection.onmessage = event => {
        try {
          const data: SSEMessage = JSON.parse(event.data);
          handleSSEMessage(data);
        } catch (error) {
          console.error('Failed to parse notification SSE message:', error);
        }
      };

      sseConnection.onerror = (error: Event) => {
        console.error('Notification SSE error:', error);
        // ReconnectingEventSource handles reconnection automatically
        // Don't reconnect if page is being unloaded or offline
        if (!globalThis.window.navigator.onLine || globalThis.document.hidden) {
          sseConnection?.close();
        }
      };
    } catch (error) {
      console.error('Failed to create ReconnectingEventSource:', error);
      // Try again in 5 seconds if initial setup fails
      setTimeout(() => connectSSE(), 5000);
    }
  }

  // Handle SSE messages
  function handleSSEMessage(data: SSEMessage) {
    switch (data.eventType) {
      case 'connected':
        // Connected to notification stream
        break;

      case 'notification':
        addNotification(data as unknown as Notification);
        break;

      case 'heartbeat':
        // Heartbeat received, connection is alive
        break;

      default:
        // Unknown SSE event type
        break;
    }
  }

  // Add new notification
  function addNotification(notification: Notification) {
    if (!shouldShowNotification(notification)) {
      return;
    }

    // Add to beginning of array
    notifications = [notification, ...notifications.slice(0, 19)];
    updateUnreadCount();

    // Wiggle animation
    if (animationTimeout) {
      globalThis.clearTimeout(animationTimeout);
    }

    hasUnread = true;
    animationTimeout = globalThis.setTimeout(() => {
      hasUnread = false;
      animationTimeout = null;
    }, 1000);

    // Play sound if enabled and notification is high priority
    if (
      soundEnabled &&
      (notification.priority === 'critical' || notification.priority === 'high')
    ) {
      playNotificationSound();
    }

    // Show browser notification if permitted
    if (notification.priority === 'critical') {
      showBrowserNotification(notification);
    }
  }

  // Update unread count
  function updateUnreadCount() {
    unreadCount = notifications.filter(n => !n.read).length;
  }

  // Mark notification as read
  async function markAsRead(notificationId: string) {
    try {
      const csrfToken =
        globalThis.document.querySelector('meta[name="csrf-token"]')?.getAttribute('content') || '';
      const response = await fetch(`/api/v2/notifications/${notificationId}/read`, {
        method: 'PUT',
        headers: {
          'X-CSRF-Token': csrfToken,
        },
      });

      if (response.ok) {
        notifications = notifications.map(n =>
          n.id === notificationId ? { ...n, read: true } : n
        );
        updateUnreadCount();
      }
    } catch {
      // Failed to mark notification as read
    }
  }

  // Mark all as read
  async function markAllAsRead() {
    const unreadIds = notifications.filter(n => !n.read).map(n => n.id);
    await Promise.all(unreadIds.map(id => markAsRead(id)));
  }

  // Handle notification click
  async function handleNotificationClick(notification: Notification) {
    if (!notification.read) {
      markAsRead(notification.id);
    }

    dropdownOpen = false;

    if (onNavigateToNotifications) {
      onNavigateToNotifications();
    } else {
      globalThis.window.location.href = '/notifications';
    }
  }

  // Play notification sound
  function playNotificationSound() {
    const audio = new globalThis.Audio('/assets/sounds/notification.mp3');
    audio.volume = 0.5;
    audio.play().catch(() => {
      // Could not play notification sound
    });
  }

  // Show browser notification
  function showBrowserNotification(notification: Notification) {
    if ('Notification' in globalThis.window && globalThis.Notification.permission === 'granted') {
      new globalThis.Notification(notification.title, {
        body: notification.message,
        icon: '/assets/images/favicon-32x32.png',
        tag: notification.id,
      });
    }
  }

  // Get notification icon class
  function getNotificationIconClass(notification: Notification): string {
    const baseClass = 'bg-opacity-20';

    switch (notification.type) {
      case 'error':
        return `${baseClass} bg-error text-error`;
      case 'warning':
        return `${baseClass} bg-warning text-warning`;
      case 'info':
        return `${baseClass} bg-info text-info`;
      case 'detection':
        return `${baseClass} bg-success text-success`;
      case 'system':
        return `${baseClass} bg-primary text-primary`;
      default:
        return `${baseClass} bg-base-300 text-base-content`;
    }
  }

  // Get priority badge class
  function getPriorityBadgeClass(priority: string): string {
    switch (priority) {
      case 'critical':
        return 'badge-error';
      case 'high':
        return 'badge-warning';
      case 'medium':
        return 'badge-info';
      case 'low':
        return 'badge-ghost';
      default:
        return 'badge-ghost';
    }
  }

  // Format time ago
  function formatTimeAgo(timestamp: string): string {
    const date = new Date(timestamp);
    const now = new Date();
    const seconds = Math.floor((now.getTime() - date.getTime()) / 1000);

    if (seconds < 60) return 'just now';
    if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
    if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
    if (seconds < 604800) return `${Math.floor(seconds / 86400)}d ago`;

    return date.toLocaleDateString();
  }


  // Handle notification deleted event
  function handleNotificationDeleted(event: CustomEvent<{ id: string; wasUnread: boolean }>) {
    const index = notifications.findIndex(n => n.id === event.detail.id);
    if (index !== -1) {
      notifications = notifications.filter(n => n.id !== event.detail.id);

      if (event.detail.wasUnread) {
        updateUnreadCount();
      }
    }
  }

  // Handle click outside
  function handleClickOutside(event: MouseEvent) {
    if (!dropdownRef || !buttonRef) return;

    const target = event.target as Node;
    if (!dropdownRef.contains(target) && !buttonRef.contains(target)) {
      dropdownOpen = false;
    }
  }

  // Cleanup
  function cleanup() {
    if (sseConnection) {
      sseConnection.close();
      sseConnection = null;
    }
    if (animationTimeout) {
      globalThis.clearTimeout(animationTimeout);
    }
  }

  onMount(() => {
    // Load sound preference
    soundEnabled = globalThis.localStorage.getItem('notificationSound') === 'true';

    // Load notifications
    loadNotifications();

    // Connect to SSE
    connectSSE();

    // Add event listeners
    globalThis.document.addEventListener('click', handleClickOutside);
    globalThis.window.addEventListener(
      'notification-deleted',
      handleNotificationDeleted as globalThis.EventListener
    );

    // Request notification permission after user interaction
    if ('Notification' in globalThis.window && globalThis.Notification.permission === 'default') {
      const requestPermission = () => {
        globalThis.Notification.requestPermission();
        globalThis.document.removeEventListener('click', requestPermission);
      };
      globalThis.document.addEventListener('click', requestPermission, { once: true });
    }

    return () => {
      globalThis.document.removeEventListener('click', handleClickOutside);
      globalThis.window.removeEventListener(
        'notification-deleted',
        handleNotificationDeleted as globalThis.EventListener
      );
    };
  });

  onDestroy(() => {
    cleanup();
  });
</script>

<div class={cn('relative', className)}>
  <button
    bind:this={buttonRef}
    onclick={() => (dropdownOpen = !dropdownOpen)}
    class="btn btn-ghost btn-sm p-1 relative"
    aria-label="Notifications"
    aria-expanded={dropdownOpen}
    aria-haspopup="menu"
    aria-controls="notification-dropdown"
  >
    <!-- Bell icon -->
    <svg
      xmlns="http://www.w3.org/2000/svg"
      fill="none"
      viewBox="0 0 24 24"
      stroke-width="1.5"
      stroke="currentColor"
      class={cn('w-6 h-6', hasUnread && 'animate-wiggle')}
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        d="M14.857 17.082a23.848 23.848 0 005.454-1.31A8.967 8.967 0 0118 9.75v-.7V9A6 6 0 006 9v.75a8.967 8.967 0 01-2.312 6.022c1.733.64 3.56 1.085 5.455 1.31m5.714 0a24.255 24.255 0 01-5.714 0m5.714 0a3 3 0 11-5.714 0"
      />
    </svg>

    <!-- Unread badge -->
    {#if !loading && unreadCount > 0}
      <span
        class="absolute -top-1 -right-1 bg-error text-error-content text-xs rounded-full px-1 min-w-[1.25rem] h-5 flex items-center justify-center font-bold"
        aria-live="polite"
        aria-atomic="true"
      >
        {unreadCount > 99 ? '99+' : unreadCount}
      </span>
    {/if}
  </button>

  <!-- Notification dropdown panel -->
  {#if dropdownOpen}
    <div
      bind:this={dropdownRef}
      id="notification-dropdown"
      role="menu"
      class="absolute right-0 mt-2 w-80 md:w-96 max-h-[32rem] bg-base-100 rounded-lg shadow-xl z-50 overflow-hidden flex flex-col"
    >
      <!-- Header -->
      <div class="flex items-center justify-between p-4 border-b border-base-300">
        <h3 class="text-lg font-semibold">Notifications</h3>
        {#if visibleNotifications.length > 0}
          <button
            onclick={markAllAsRead}
            class="text-sm link link-primary"
            aria-label="Mark all notifications as read"
          >
            Mark all as read
          </button>
        {/if}
      </div>

      <!-- Notification list -->
      <div class="overflow-y-auto flex-1">
        {#if loading}
          <!-- Loading state -->
          <div class="p-8 text-center">
            <div class="loading loading-spinner loading-md" role="status">
              <span class="sr-only">Loading notifications...</span>
            </div>
          </div>
        {:else if visibleNotifications.length === 0}
          <!-- Empty state -->
          <div class="p-8 text-center text-base-content/60">
            <svg
              xmlns="http://www.w3.org/2000/svg"
              fill="none"
              viewBox="0 0 24 24"
              stroke-width="1.5"
              stroke="currentColor"
              class="w-12 h-12 mx-auto mb-2 opacity-50"
              role="img"
              aria-label="No notifications"
            >
              <path
                stroke-linecap="round"
                stroke-linejoin="round"
                d="M9.143 17.082a24.248 24.248 0 003.844.148m-3.844-.148a23.856 23.856 0 01-5.455-1.31 8.964 8.964 0 002.3-5.542m3.155 6.852a3 3 0 005.667 1.97m1.965-2.277L21 21m-4.225-4.225a23.81 23.81 0 003.536-1.003A8.967 8.967 0 0118 9.75V9A6 6 0 006.53 6.53m10.245 10.245L6.53 6.53M3 3l3.53 3.53"
              />
            </svg>
            <p>No notifications</p>
          </div>
        {:else}
          <!-- Notifications -->
          {#each visibleNotifications as notification (notification.id)}
            <div
              role="menuitem"
              class={cn(
                'border-b border-base-300 p-4 hover:bg-base-200 transition-colors cursor-pointer',
                !notification.read && 'bg-base-200/50'
              )}
              onclick={() => handleNotificationClick(notification)}
              onkeydown={e => {
                if (e.key === 'Enter' || e.key === ' ') {
                  e.preventDefault();
                  handleNotificationClick(notification);
                }
              }}
              tabindex="0"
            >
              <!-- Notification icon based on type -->
              <div class="flex items-start gap-3">
                <div class="flex-shrink-0 mt-1">
                  <div
                    class={cn(
                      'w-8 h-8 rounded-full flex items-center justify-center',
                      getNotificationIconClass(notification)
                    )}
                  >
                    {#if notification.type === 'error'}
                      <svg
                        xmlns="http://www.w3.org/2000/svg"
                        fill="none"
                        viewBox="0 0 24 24"
                        stroke-width="2"
                        stroke="currentColor"
                        class="w-5 h-5"
                      >
                        <path
                          stroke-linecap="round"
                          stroke-linejoin="round"
                          d="M12 9v3.75m9-.75a9 9 0 11-18 0 9 9 0 0118 0zm-9 3.75h.008v.008H12v-.008z"
                        />
                      </svg>
                    {:else if notification.type === 'warning'}
                      <svg
                        xmlns="http://www.w3.org/2000/svg"
                        fill="none"
                        viewBox="0 0 24 24"
                        stroke-width="2"
                        stroke="currentColor"
                        class="w-5 h-5"
                      >
                        <path
                          stroke-linecap="round"
                          stroke-linejoin="round"
                          d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126zM12 15.75h.007v.008H12v-.008z"
                        />
                      </svg>
                    {:else if notification.type === 'info'}
                      <svg
                        xmlns="http://www.w3.org/2000/svg"
                        fill="none"
                        viewBox="0 0 24 24"
                        stroke-width="2"
                        stroke="currentColor"
                        class="w-5 h-5"
                      >
                        <path
                          stroke-linecap="round"
                          stroke-linejoin="round"
                          d="m11.25 11.25.041-.02a.75.75 0 011.063.852l-.708 2.836a.75.75 0 001.063.853l.041-.021M21 12a9 9 0 11-18 0 9 9 0 0118 0zm-9-3.75h.008v.008H12V8.25z"
                        />
                      </svg>
                    {:else if notification.type === 'detection'}
                      <svg
                        xmlns="http://www.w3.org/2000/svg"
                        fill="none"
                        viewBox="0 0 24 24"
                        stroke-width="2"
                        stroke="currentColor"
                        class="w-5 h-5"
                      >
                        <path
                          stroke-linecap="round"
                          stroke-linejoin="round"
                          d="M11.48 3.499a.562.562 0 011.04 0l2.125 5.111a.563.563 0 00.475.345l5.518.442c.499.04.701.663.321.988l-4.204 3.602a.563.563 0 00-.182.557l1.285 5.385a.562.562 0 01-.84.61l-4.725-2.885a.563.563 0 00-.586 0L6.982 20.54a.562.562 0 01-.84-.61l1.285-5.386a.562.562 0 00-.182-.557l-4.204-3.602a.563.563 0 01.321-.988l5.518-.442a.563.563 0 00.475-.345L11.48 3.5z"
                        />
                      </svg>
                    {:else if notification.type === 'system'}
                      <svg
                        xmlns="http://www.w3.org/2000/svg"
                        fill="none"
                        viewBox="0 0 24 24"
                        stroke-width="2"
                        stroke="currentColor"
                        class="w-5 h-5"
                      >
                        <path
                          stroke-linecap="round"
                          stroke-linejoin="round"
                          d="M9.594 3.94c.09-.542.56-.94 1.11-.94h2.593c.55 0 1.02.398 1.11.94l.213 1.281c.063.374.313.686.645.87.074.04.147.083.22.127.324.196.72.257 1.075.124l1.217-.456a1.125 1.125 0 011.37.49l1.296 2.247a1.125 1.125 0 01-.26 1.431l-1.003.827c-.293.24-.438.613-.431.992a6.759 6.759 0 010 .255c-.007.378.138.75.43.99l1.005.828c.424.35.534.954.26 1.43l-1.298 2.247a1.125 1.125 0 01-1.369.491l-1.217-.456c-.355-.133-.75-.072-1.076.124a6.57 6.57 0 01-.22.128c-.331.183-.581.495-.644.869l-.213 1.28c-.09.543-.56.941-1.11.941h-2.594c-.55 0-1.02-.398-1.11-.94l-.213-1.281c-.062-.374-.312-.686-.644-.87a6.52 6.52 0 01-.22-.127c-.325-.196-.72-.257-1.076-.124l-1.217.456a1.125 1.125 0 01-1.369-.49l-1.297-2.247a1.125 1.125 0 01.26-1.431l1.004-.827c.292-.24.437-.613.43-.992a6.932 6.932 0 010-.255c.007-.378-.138-.75-.43-.99l-1.004-.828a1.125 1.125 0 01-.26-1.43l1.297-2.247a1.125 1.125 0 011.37-.491l1.216.456c.356.133.751.072 1.076-.124.072-.044.146-.087.22-.128.332-.183.582-.495.644-.869l.214-1.281z"
                        />
                        <path
                          stroke-linecap="round"
                          stroke-linejoin="round"
                          d="M15 12a3 3 0 11-6 0 3 3 0 016 0z"
                        />
                      </svg>
                    {/if}
                  </div>
                </div>
                <div class="flex-1 min-w-0">
                  <div class="flex items-start justify-between gap-2">
                    <h4 class="font-medium text-sm truncate">{notification.title}</h4>
                    <time
                      class="text-xs text-base-content/60 whitespace-nowrap"
                      datetime={notification.timestamp}
                    >
                      {formatTimeAgo(notification.timestamp)}
                    </time>
                  </div>
                  <p class="text-sm text-base-content/80 mt-1">{notification.message}</p>
                  <div class="flex items-center gap-2 mt-2">
                    {#if notification.component}
                      <span class="badge badge-sm badge-ghost">{notification.component}</span>
                    {/if}
                    <span
                      class={cn('badge badge-sm', getPriorityBadgeClass(notification.priority))}
                    >
                      {notification.priority}
                    </span>
                  </div>
                </div>
              </div>
            </div>
          {/each}
        {/if}
      </div>

      <!-- Footer -->
      <div class="p-4 border-t border-base-300">
        <button
          onclick={() => {
            dropdownOpen = false;
            if (onNavigateToNotifications) {
              onNavigateToNotifications();
            } else {
              globalThis.window.location.href = '/notifications';
            }
          }}
          class="btn btn-sm btn-block btn-ghost"
        >
          View all notifications
        </button>
      </div>
    </div>
  {/if}
</div>

<style>
  @keyframes wiggle {
    0%,
    100% {
      transform: rotate(0deg);
    }
    25% {
      transform: rotate(-5deg);
    }
    75% {
      transform: rotate(5deg);
    }
  }

  .animate-wiggle {
    animation: wiggle 0.3s ease-in-out 2;
  }
</style>
