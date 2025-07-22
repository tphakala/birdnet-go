<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import ReconnectingEventSource from 'reconnecting-eventsource';
  import { cn } from '$lib/utils/cn';
  import { api, ApiError } from '$lib/utils/api';
  import { toastActions } from '$lib/stores/toast';
  import { alertIcons, alertIconsSvg, systemIcons } from '$lib/utils/icons';

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
      const data = await api.get<{ notifications?: Notification[] }>('/api/v2/notifications?limit=20&status=unread');
      notifications = (data?.notifications || []).filter((n: Notification) =>
        shouldShowNotification(n)
      );
      updateUnreadCount();
    } catch (error) {
      // Only show user-facing error for notification loading failures since users expect to see notifications
      if (error instanceof ApiError) {
        toastActions.error('Unable to load notifications. Please refresh the page.');
      }
      // Log for developers without cluttering console in production
      if (process.env.NODE_ENV === 'development') {
        console.error('Failed to load notifications:', error);
      }
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
        withCredentials: false,
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
      await api.put(`/api/v2/notifications/${notificationId}/read`);
      notifications = notifications.map(n =>
        n.id === notificationId ? { ...n, read: true } : n
      );
      updateUnreadCount();
    } catch (error) {
      // Show user feedback for failed mark-as-read since this is a user action
      if (error instanceof ApiError) {
        toastActions.error('Failed to mark notification as read.');
      }
      if (process.env.NODE_ENV === 'development') {
        console.error('Failed to mark notification as read:', error);
      }
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
    <div class={cn('w-6 h-6', hasUnread && 'animate-wiggle')}>
      {@html systemIcons.bell}
    </div>

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
      class="absolute right-0 top-full mt-2 min-w-[20rem] w-80 md:w-96 max-w-[calc(100vw-1rem)] max-h-[32rem] bg-base-100 rounded-lg shadow-xl border border-base-300 z-50 overflow-hidden flex flex-col"
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
            <div class="w-12 h-12 mx-auto mb-2 opacity-50" role="img" aria-label="No notifications">
              {@html systemIcons.bellOff}
            </div>
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
                      <div class="w-5 h-5">
                        {@html alertIconsSvg.error}
                      </div>
                    {:else if notification.type === 'warning'}
                      <div class="w-5 h-5">
                        {@html alertIconsSvg.warning}
                      </div>
                    {:else if notification.type === 'info'}
                      <div class="w-5 h-5">
                        {@html alertIconsSvg.info}
                      </div>
                    {:else if notification.type === 'detection'}
                      <div class="w-5 h-5">
                        {@html systemIcons.star}
                      </div>
                    {:else if notification.type === 'system'}
                      <div class="w-5 h-5">
                        {@html systemIcons.settingsGear}
                      </div>
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
