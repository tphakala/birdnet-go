<script lang="ts">
  import { untrack } from 'svelte';
  import { cn } from '$lib/utils/cn';
  import { t } from '$lib/i18n';
  import type {
    NotificationGroup as NotificationGroupType,
    Notification,
  } from '$lib/utils/notifications';
  import { sanitizeNotificationMessage } from '$lib/utils/notifications';
  import {
    ChevronDown,
    XCircle,
    TriangleAlert,
    Info,
    Star,
    Settings,
    Eye,
    Check,
    Trash2,
  } from '@lucide/svelte';

  interface Props {
    group: NotificationGroupType;
    defaultOpen?: boolean;
    onMarkAllRead?: (_ids: string[]) => void;
    onDismissAll?: (_ids: string[]) => void;
    onMarkAsRead?: (_id: string) => void;
    onAcknowledge?: (_id: string) => void;
    onDelete?: (_id: string) => void;
    onNotificationClick?: (_notification: Notification) => void;
    className?: string;
  }

  let {
    group,
    defaultOpen = false,
    onMarkAllRead,
    onDismissAll,
    onMarkAsRead,
    onAcknowledge,
    onDelete,
    onNotificationClick,
    className = '',
  }: Props = $props();

  // Use untrack to explicitly capture initial value without creating dependency
  let isOpen = $state(untrack(() => defaultOpen));

  function toggleOpen() {
    isOpen = !isOpen;
  }

  // Derived values
  let count = $derived(group.notifications.length);
  let hasMultiple = $derived(count > 1);
  let unreadIds = $derived(group.notifications.filter(n => !n.read).map(n => n.id));
  let allIds = $derived(group.notifications.map(n => n.id));

  // Time range display
  let timeRange = $derived.by(() => {
    if (!hasMultiple) return formatTime(group.latestTimestamp);
    const earliest = formatTime(group.earliestTimestamp);
    const latest = formatTime(group.latestTimestamp);
    if (earliest === latest) return latest;
    return `${earliest} - ${latest}`;
  });

  function formatTime(timestamp: string): string {
    const date = new Date(timestamp);
    const now = new Date();
    const diff = now.getTime() - date.getTime();

    if (diff < 60000) return t('notifications.timeAgo.justNow');
    if (diff < 3600000)
      return t('notifications.timeAgo.minutesAgo', { minutes: Math.floor(diff / 60000) });
    if (diff < 86400000)
      return t('notifications.timeAgo.hoursAgo', { hours: Math.floor(diff / 3600000) });
    if (diff < 604800000)
      return t('notifications.timeAgo.daysAgo', { days: Math.floor(diff / 86400000) });

    return date.toLocaleDateString();
  }

  function getTypeIcon(type: string) {
    switch (type) {
      case 'error':
        return XCircle;
      case 'warning':
        return TriangleAlert;
      case 'info':
        return Info;
      case 'detection':
        return Star;
      default:
        return Settings;
    }
  }

  function getIconClass(type: string): string {
    const classes: Record<string, string> = {
      error: 'bg-error/20 text-error',
      warning: 'bg-warning/20 text-warning',
      info: 'bg-info/20 text-info',
      detection: 'bg-success/20 text-success',
      system: 'bg-primary/20 text-primary',
    };
    return classes[type] ?? 'bg-base-300';
  }

  function getPriorityBadgeClass(priority: string): string {
    const classes: Record<string, string> = {
      critical: 'badge-error',
      high: 'badge-warning',
      medium: 'badge-info',
      low: 'badge-ghost',
    };
    return classes[priority] ?? 'badge-ghost';
  }

  function isClickable(notification: Notification): boolean {
    return notification.type === 'detection' && !!notification.metadata?.note_id;
  }

  function handleNotificationClick(notification: Notification) {
    if (isClickable(notification) && onNotificationClick) {
      onNotificationClick(notification);
    }
  }
</script>

<div
  class={cn(
    'card bg-base-100 shadow-2xs',
    group.unreadCount > 0 && 'border-l-4 border-primary',
    className
  )}
>
  <!-- Group Header (always visible) -->
  <div class="flex items-center gap-3 p-3 hover:bg-base-200/50 transition-colors">
    <!-- Clickable area for toggle -->
    <button
      type="button"
      class="flex items-center gap-3 flex-1 min-w-0 text-left"
      onclick={toggleOpen}
      aria-expanded={isOpen}
      aria-controls="group-content-{group.key}"
    >
      <!-- Type Icon -->
      {#if true}
        {@const IconComponent = getTypeIcon(group.type)}
        <div
          class={cn(
            'w-8 h-8 rounded-full flex items-center justify-center shrink-0',
            getIconClass(group.type)
          )}
        >
          <IconComponent class="size-4" />
        </div>
      {/if}

      <!-- Title & Meta -->
      <div class="flex-1 min-w-0">
        <div class="flex items-center gap-2">
          <h3 class="font-medium text-sm truncate">{group.title}</h3>
          {#if hasMultiple}
            <span class="badge badge-sm badge-primary">{count}</span>
          {/if}
        </div>
        <div class="flex flex-wrap items-center gap-2 mt-1 text-xs text-base-content/60">
          {#if group.component}
            <span class="badge badge-ghost badge-xs">{group.component}</span>
          {/if}
          <span class="badge badge-xs {getPriorityBadgeClass(group.highestPriority)}">
            {group.highestPriority}
          </span>
          <time datetime={group.latestTimestamp}>{timeRange}</time>
          {#if group.unreadCount > 0}
            <span class="text-primary font-medium"
              >{t('notifications.groups.unread', { count: group.unreadCount })}</span
            >
          {/if}
        </div>
      </div>

      <!-- Chevron -->
      <div class={cn('transition-transform duration-200 shrink-0', isOpen && 'rotate-180')}>
        <ChevronDown class="size-4 opacity-60" />
      </div>
    </button>

    <!-- Group Actions (separate from toggle button) -->
    {#if hasMultiple}
      <div class="flex gap-1 shrink-0">
        {#if group.unreadCount > 0 && onMarkAllRead}
          <button
            onclick={() => onMarkAllRead?.(unreadIds)}
            class="btn btn-ghost btn-xs"
            aria-label={t('notifications.groups.markAllRead')}
          >
            <Eye class="size-3" />
          </button>
        {/if}
        {#if onDismissAll}
          <button
            onclick={() => onDismissAll?.(allIds)}
            class="btn btn-ghost btn-xs text-error"
            aria-label={t('notifications.groups.dismissAll')}
          >
            <Trash2 class="size-3" />
          </button>
        {/if}
      </div>
    {/if}
  </div>

  <!-- Expanded Content -->
  {#if isOpen}
    <div id="group-content-{group.key}" class="border-t border-base-200">
      {#each group.notifications as notification (notification.id)}
        <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
        <div
          class={cn(
            'p-3 pl-14 border-b border-base-100 last:border-b-0 hover:bg-base-200/30 transition-colors',
            !notification.read && 'bg-base-200/20',
            isClickable(notification) && 'cursor-pointer'
          )}
          onclick={() => handleNotificationClick(notification)}
          role={isClickable(notification) ? 'button' : undefined}
          tabindex={isClickable(notification) ? 0 : undefined}
          onkeydown={e => {
            if (isClickable(notification) && (e.key === 'Enter' || e.key === ' ')) {
              e.preventDefault();
              handleNotificationClick(notification);
            }
          }}
        >
          <div class="flex items-start justify-between gap-2">
            <div class="flex-1 min-w-0">
              <p class="text-xs text-base-content/80">
                {sanitizeNotificationMessage(notification.message)}
              </p>
              <time
                class="text-xs text-base-content/50 mt-1 block"
                datetime={notification.timestamp}
              >
                {formatTime(notification.timestamp)}
              </time>
            </div>

            <!-- Individual Actions -->
            <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
            <div class="flex items-center gap-1 shrink-0" onclick={e => e.stopPropagation()}>
              {#if !notification.read && onMarkAsRead}
                <button
                  onclick={() => onMarkAsRead?.(notification.id)}
                  class="btn btn-ghost btn-xs"
                  aria-label={t('notifications.actions.markAsRead')}
                >
                  <Eye class="size-3" />
                </button>
              {/if}
              {#if notification.read && notification.status !== 'acknowledged' && onAcknowledge}
                <button
                  onclick={() => onAcknowledge?.(notification.id)}
                  class="btn btn-ghost btn-xs"
                  aria-label={t('notifications.actions.acknowledge')}
                >
                  <Check class="size-3" />
                </button>
              {/if}
              {#if onDelete}
                <button
                  onclick={() => onDelete?.(notification.id)}
                  class="btn btn-ghost btn-xs text-error"
                  aria-label={t('notifications.actions.delete')}
                >
                  <Trash2 class="size-3" />
                </button>
              {/if}
            </div>
          </div>
        </div>
      {/each}
    </div>
  {/if}
</div>
