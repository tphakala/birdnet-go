<script lang="ts">
  import { sseNotifications } from '$lib/stores/sseNotifications';
  import { cn } from '$lib/utils/cn';
  import { api, ApiError } from '$lib/utils/api';
  import { toastActions } from '$lib/stores/toast';
  import { Bell, BellOff, XCircle, TriangleAlert, Info, Settings, Star } from '@lucide/svelte';
  import { loggers } from '$lib/utils/logger';
  import {
    type Notification,
    mergeAndDeduplicateNotifications,
    isExistingNotification,
    shouldShowNotification,
    sanitizeNotificationMessage,
    mapApiNotification,
    mapApiNotifications,
  } from '$lib/utils/notifications';

  const logger = loggers.ui;

  // Time calculation constants
  const MS_PER_MINUTE = 60000;
  const MINUTES_PER_HOUR = 60;
  const MINUTES_PER_DAY = 1440;

  // UI constants
  const ANIMATION_DURATION_MS = 1000;
  const NOTIFICATION_VOLUME = 0.5;
  const NOTIFICATION_SOUND_PATH = '/ui/assets/sounds/notification.mp3';
  const NOTIFICATION_ICON_PATH = '/ui/assets/favicon-32x32.png';
  const NOTIFICATIONS_PAGE_URL = '/notifications';
  const SOUND_ENABLED_KEY = 'notificationSound';
  const NOTIFICATIONS_LIMIT = 20;
  const BADGE_MAX_COUNT = 99;
  const DROPDOWN_Z_INDEX = 1010;

  interface Props {
    className?: string;
    debugMode?: boolean;
    onNavigateToNotifications?: () => void;
  }

  let { className = '', debugMode = false, onNavigateToNotifications }: Props = $props();

  // State
  let notifications = $state<Notification[]>([]);
  let dropdownOpen = $state(false);
  let loading = $state(true);
  let hasUnread = $state(false);
  let soundEnabled = $state(false);
  let isAuthenticated = $state(true); // Assume authenticated until proven otherwise

  // Derived state - automatically updates when notifications change
  let unreadCount = $derived(notifications.filter(n => !n.read).length);

  // Internal state
  let unsubscribeSSE: (() => void) | null = null;
  let animationTimeout: ReturnType<typeof globalThis.setTimeout> | null = null;
  let dropdownRef = $state<HTMLDivElement>();
  let buttonRef = $state<HTMLButtonElement>();

  // Filtered notifications for display
  const visibleNotifications = $derived(
    notifications.filter(n => shouldShowNotification(n, debugMode))
  );

  // Pre-computed display data for notifications
  const formattedNotifications = $derived(
    visibleNotifications.map(notification => ({
      ...notification,
      timeAgo: formatTimeAgo(notification.timestamp),
      iconClass: getNotificationIconClass(notification),
      priorityBadgeClass: getPriorityBadgeClass(notification.priority),
    }))
  );

  function formatTimeAgo(timestamp: string): string {
    const now = new Date();
    const time = new Date(timestamp);
    const diffMs = now.getTime() - time.getTime();
    const diffMins = Math.max(0, Math.floor(diffMs / MS_PER_MINUTE));

    if (diffMins < 1) return 'just now';
    if (diffMins < MINUTES_PER_HOUR) return `${diffMins}m ago`;
    if (diffMins < MINUTES_PER_DAY) return `${Math.floor(diffMins / MINUTES_PER_HOUR)}h ago`;
    return `${Math.floor(diffMins / MINUTES_PER_DAY)}d ago`;
  }

  function getNotificationIconClass(notification: Notification): string {
    switch (notification.type) {
      case 'error':
        return 'bg-error/20 text-error';
      case 'warning':
        return 'bg-warning/20 text-warning';
      case 'detection':
        return 'bg-success/20 text-success';
      case 'system':
        return 'bg-primary/20 text-primary';
      case 'info':
        return 'bg-info/20 text-info';
      default:
        return 'bg-base-300 text-base-content';
    }
  }

  function getPriorityBadgeClass(priority: string): string {
    switch (priority) {
      case 'critical':
        return 'badge-error';
      case 'high':
        return 'badge-warning';
      case 'medium':
        return 'badge-info';
      default:
        return 'badge-ghost';
    }
  }

  // Load notifications from API
  async function loadNotifications() {
    loading = true;
    try {
      const data = await api.get<{ notifications?: Notification[] }>(
        `/api/v2/notifications?limit=${NOTIFICATIONS_LIMIT}&status=unread`
      );
      // Map API notifications to frontend format (status -> read)
      const apiNotifications = mapApiNotifications(data?.notifications ?? []);

      // Apply deduplication to API-fetched notifications
      // This ensures consistent deduplication behavior between SSE and API
      notifications = mergeAndDeduplicateNotifications(notifications, apiNotifications, {
        debugMode,
      });
    } catch (error) {
      // Handle authentication errors gracefully - expected for non-authenticated users
      if (error instanceof ApiError && error.status === 401) {
        // Silently fail for unauthenticated users - this is expected behavior
        isAuthenticated = false;
        notifications = [];
        return;
      }

      // Only show user-facing error for non-auth notification loading failures
      if (error instanceof ApiError) {
        toastActions.error('Unable to load notifications. Please refresh the page.');
      }
      // Log error for debugging - logger already handles dev/prod environment
      logger.error('Failed to load notifications', error, {
        component: 'NotificationBell',
        action: 'loadNotifications',
      });
    } finally {
      loading = false;
    }
  }

  // Subscribe to SSE notifications via singleton
  function subscribeToNotifications() {
    // Don't subscribe for non-authenticated users
    if (!isAuthenticated) {
      return;
    }

    // Clean up existing subscription
    if (unsubscribeSSE) {
      unsubscribeSSE();
      unsubscribeSSE = null;
    }

    // Register callback with singleton - validation is handled by sseNotifications
    unsubscribeSSE = sseNotifications.registerNotificationCallback(addNotification);

    logger.debug('Subscribed to notification stream via singleton');
  }

  // Add single notification (used for SSE)
  // Note: sseNotifications singleton validates notifications before invoking callbacks
  function addNotification(notification: Notification) {
    // Map SSE notification from API format (status -> read)
    const mappedNotification = mapApiNotification(notification);

    // Check if notification already exists BEFORE merging
    const wasNewNotification = !isExistingNotification(mappedNotification, notifications);

    // Always perform merge to update timestamps and priority
    notifications = mergeAndDeduplicateNotifications(notifications, [mappedNotification], {
      debugMode,
    });

    // Only trigger UI effects for truly new notifications
    if (wasNewNotification) {
      // Wiggle animation
      if (animationTimeout) {
        globalThis.clearTimeout(animationTimeout);
      }

      hasUnread = true;
      animationTimeout = globalThis.setTimeout(() => {
        hasUnread = false;
        animationTimeout = null;
      }, ANIMATION_DURATION_MS);

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
  }

  // Mark notification as read
  async function markAsRead(notificationId: string) {
    try {
      await api.put(`/api/v2/notifications/${notificationId}/read`);
      notifications = notifications.map(n => (n.id === notificationId ? { ...n, read: true } : n));
    } catch (error) {
      // Show user feedback for failed mark-as-read since this is a user action
      if (error instanceof ApiError) {
        toastActions.error('Failed to mark notification as read.');
      }
      logger.error('Failed to mark notification as read', error, {
        component: 'NotificationBell',
        action: 'markAsRead',
        notificationId,
      });
    }
  }

  // Mark all as read
  async function markAllAsRead() {
    const unreadIds = notifications.filter(n => !n.read).map(n => n.id);
    await Promise.all(unreadIds.map(id => markAsRead(id)));
  }

  // Navigate to notifications page
  function navigateToNotifications() {
    dropdownOpen = false;
    if (onNavigateToNotifications) {
      onNavigateToNotifications();
    } else {
      globalThis.window.location.href = NOTIFICATIONS_PAGE_URL;
    }
  }

  // Handle notification click
  async function handleNotificationClick(notification: Notification) {
    if (!notification.read) {
      await markAsRead(notification.id);
    }
    navigateToNotifications();
  }

  let audioReady = false;
  let preloadedAudio: HTMLAudioElement | null = null;

  // Preload notification sound
  function preloadNotificationSound() {
    try {
      const audio = new globalThis.Audio(NOTIFICATION_SOUND_PATH);
      audio.volume = NOTIFICATION_VOLUME;
      audio.preload = 'auto';

      audio.addEventListener('canplaythrough', () => {
        audioReady = true;
        preloadedAudio = audio;
      });

      audio.addEventListener('error', e => {
        logger.warn('Failed to load notification sound', null, {
          component: 'NotificationBell',
          action: 'preloadNotificationSound',
          errorType: e.type,
          target: e.target?.constructor?.name || 'HTMLAudioElement',
        });
        audioReady = false;
        preloadedAudio = null;
      });

      audio.load();
    } catch (error) {
      logger.warn('Failed to preload notification sound', error, {
        component: 'NotificationBell',
        action: 'preloadNotificationSound',
      });
      audioReady = false;
      preloadedAudio = null;
    }
  }

  // Play notification sound
  function playNotificationSound() {
    if (preloadedAudio && audioReady) {
      // Use preloaded audio for faster playback
      preloadedAudio.currentTime = 0;
      preloadedAudio.play().catch(error => {
        logger.debug('Could not play preloaded notification sound', error, {
          component: 'NotificationBell',
          action: 'playNotificationSound',
          mode: 'preloaded',
        });
      });
    } else {
      // Fallback to creating new Audio instance
      const audio = new globalThis.Audio(NOTIFICATION_SOUND_PATH);
      audio.volume = NOTIFICATION_VOLUME;
      audio.play().catch(error => {
        logger.debug('Could not play notification sound', error, {
          component: 'NotificationBell',
          action: 'playNotificationSound',
          mode: 'new-audio',
        });
      });
    }
  }

  // Show browser notification
  function showBrowserNotification(notification: Notification) {
    if ('Notification' in globalThis.window && globalThis.Notification.permission === 'granted') {
      new globalThis.Notification(sanitizeNotificationMessage(notification.title), {
        body: sanitizeNotificationMessage(notification.message),
        icon: NOTIFICATION_ICON_PATH,
        tag: notification.id,
      });
    }
  }

  // Handle notification deleted event
  function handleNotificationDeleted(event: CustomEvent<{ id: string; wasUnread: boolean }>) {
    notifications = notifications.filter(n => n.id !== event.detail.id);
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
    if (unsubscribeSSE) {
      unsubscribeSSE();
      unsubscribeSSE = null;
    }
    if (animationTimeout) {
      globalThis.clearTimeout(animationTimeout);
      animationTimeout = null;
    }
    // Clean up audio resources - abort any ongoing download
    if (preloadedAudio) {
      preloadedAudio.src = '';
      preloadedAudio.load();
      preloadedAudio = null;
      audioReady = false;
    }
  }

  $effect(() => {
    if (typeof globalThis.window !== 'undefined') {
      // Load sound preference
      soundEnabled = globalThis.localStorage.getItem(SOUND_ENABLED_KEY) === 'true';

      // Preload notification sound
      preloadNotificationSound();

      // Load notifications first
      loadNotifications().then(() => {
        // Subscribe to SSE notifications if authenticated
        if (isAuthenticated) {
          subscribeToNotifications();
        }
      });

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
        cleanup();
      };
    }
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
    <div class={cn(hasUnread && 'animate-wiggle')}>
      <Bell class="size-6" />
    </div>

    <!-- Unread badge -->
    {#if !loading && unreadCount > 0}
      <span
        class="absolute -top-1 -right-1 bg-error text-error-content text-xs rounded-full px-1 min-w-5 h-5 flex items-center justify-center font-bold"
        aria-live="polite"
        aria-atomic="true"
      >
        {unreadCount > BADGE_MAX_COUNT ? `${BADGE_MAX_COUNT}+` : unreadCount}
      </span>
    {/if}
  </button>

  <!-- Notification dropdown panel -->
  {#if dropdownOpen}
    <div
      bind:this={dropdownRef}
      id="notification-dropdown"
      role={!loading && formattedNotifications.length > 0 ? 'menu' : undefined}
      class="notification-dropdown absolute top-full mt-2 w-80 sm:w-96 max-w-[calc(100vw-2rem)] max-h-128 bg-base-100 rounded-lg shadow-xl border border-base-300 overflow-hidden flex flex-col"
      style:z-index={DROPDOWN_Z_INDEX}
    >
      <!-- Header -->
      <div class="flex items-center justify-between p-4 border-b border-base-300">
        <h3 class="text-lg font-semibold">Notifications</h3>
        {#if formattedNotifications.length > 0}
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
        {:else if formattedNotifications.length === 0}
          <!-- Empty state -->
          <div class="p-8 text-center text-base-content/60">
            <div class="mx-auto mb-2 opacity-50" role="img" aria-label="No notifications">
              <BellOff class="size-12" />
            </div>
            <p>No notifications</p>
          </div>
        {:else}
          <!-- Notifications -->
          {#each formattedNotifications as notification (notification.id)}
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
                <div class="shrink-0 mt-1">
                  <div
                    class={cn(
                      'w-8 h-8 rounded-full flex items-center justify-center',
                      notification.iconClass
                    )}
                  >
                    {#if notification.type === 'error'}
                      <XCircle class="size-5" />
                    {:else if notification.type === 'warning'}
                      <TriangleAlert class="size-5" />
                    {:else if notification.type === 'info'}
                      <Info class="size-5" />
                    {:else if notification.type === 'detection'}
                      <Star class="size-5" />
                    {:else if notification.type === 'system'}
                      <Settings class="size-5" />
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
                      {notification.timeAgo}
                    </time>
                  </div>
                  <p class="text-sm text-base-content/80 mt-1">
                    {sanitizeNotificationMessage(notification.message)}
                  </p>
                  <div class="flex items-center gap-2 mt-2">
                    {#if notification.component}
                      <span class="badge badge-sm badge-ghost">{notification.component}</span>
                    {/if}
                    <span class={cn('badge badge-sm', notification.priorityBadgeClass)}>
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
        <button onclick={navigateToNotifications} class="btn btn-sm btn-block btn-ghost">
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

  /* Mobile: fixed positioning centered horizontally to prevent overflow */
  .notification-dropdown {
    position: fixed;
    left: 50%;
    right: auto;
    transform: translateX(-50%);
    top: 4rem; /* Below header */
  }

  /* Desktop (sm+): absolute positioning aligned to bell icon */
  @media (min-width: 640px) {
    .notification-dropdown {
      position: absolute;
      left: auto;
      right: 0;
      transform: none;
      top: 100%;
    }
  }
</style>
