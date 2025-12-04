<script lang="ts">
  import { t } from '$lib/i18n';
  import { systemIcons, navigationIcons } from '$lib/utils/icons';

  interface Notification {
    id: string;
    type: 'new_species' | 'daily_summary' | 'system_alert';
    title: string;
    message: string;
    timestamp: string;
    read: boolean;
    link?: string;
  }

  interface GroupedNotifications {
    date: string;
    label: string;
    items: Notification[];
  }

  let notifications = $state<Notification[]>([]);
  let loading = $state(true);
  // Note: _error is kept for future error display UI but currently unused
  // eslint-disable-next-line no-unused-vars
  let _error = $state<string | null>(null);

  let grouped = $derived.by(() => {
    const groups = new Map<string, Notification[]>();
    const today = new Date().toDateString();
    const yesterday = new Date(Date.now() - 86400000).toDateString();

    for (const n of notifications) {
      const date = new Date(n.timestamp).toDateString();
      if (!groups.has(date)) {
        groups.set(date, []);
      }
      groups.get(date)!.push(n);
    }

    const result: GroupedNotifications[] = [];
    for (const [date, items] of groups) {
      let label = date;
      if (date === today) label = 'Today';
      else if (date === yesterday) label = 'Yesterday';
      result.push({ date, label, items });
    }

    return result;
  });

  function getNotificationIcon(type: Notification['type']): string {
    switch (type) {
      case 'new_species':
        return systemIcons.bird;
      case 'daily_summary':
        return systemIcons.list;
      case 'system_alert':
        return systemIcons.bell;
      default:
        return systemIcons.bell;
    }
  }

  function formatTime(timestamp: string): string {
    return new Date(timestamp).toLocaleTimeString([], {
      hour: '2-digit',
      minute: '2-digit',
    });
  }

  function getCsrfToken(): string {
    const meta = document.querySelector('meta[name="csrf-token"]');
    return meta?.getAttribute('content') ?? '';
  }

  async function loadNotifications(): Promise<void> {
    loading = true;
    _error = null;

    try {
      const csrfToken = getCsrfToken();
      const response = await fetch('/api/v2/notifications', {
        headers: { 'X-CSRF-Token': csrfToken },
      });

      if (response.ok) {
        notifications = await response.json();
      } else if (response.status === 404) {
        // Notifications API may not exist yet
        notifications = [];
      } else {
        throw new Error(`Server responded with ${response.status}`);
      }
    } catch {
      // Gracefully handle missing notifications endpoint
      notifications = [];
      _error = null;
    } finally {
      loading = false;
    }
  }

  function handleNotificationClick(notification: Notification): void {
    if (notification.link) {
      window.location.href = notification.link;
    }
  }

  $effect(() => {
    loadNotifications();
  });
</script>

<div class="flex flex-col gap-4 p-4 pb-20">
  {#if loading}
    <div class="flex justify-center py-8">
      <span class="loading loading-spinner loading-lg"></span>
    </div>
  {:else if notifications.length === 0}
    <div class="card bg-base-100 shadow-sm">
      <div class="card-body items-center py-12 text-center">
        <div
          class="flex h-16 w-16 items-center justify-center rounded-full bg-base-200 text-base-content/40"
        >
          {@html systemIcons.bell}
        </div>
        <p class="mt-4 text-base-content/60">{t('notifications.empty')}</p>
      </div>
    </div>
  {:else}
    {#each grouped as group (group.date)}
      <div>
        <h2 class="mb-2 px-1 text-sm font-semibold text-base-content/60">
          {group.label}
        </h2>
        <div class="card divide-y divide-base-200 bg-base-100 shadow-sm">
          {#each group.items as notification (notification.id)}
            <button
              class="flex w-full items-start gap-3 p-4 text-left hover:bg-base-200/50 active:bg-base-200"
              class:opacity-60={notification.read}
              onclick={() => handleNotificationClick(notification)}
            >
              <div
                class="flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-primary/20 text-primary"
              >
                {@html getNotificationIcon(notification.type)}
              </div>
              <div class="min-w-0 flex-1">
                <div class="font-medium">{notification.title}</div>
                <div class="truncate text-sm text-base-content/60">
                  {notification.message}
                </div>
                <div class="mt-1 text-xs text-base-content/40">
                  {formatTime(notification.timestamp)}
                </div>
              </div>
              {#if notification.link}
                <span class="text-base-content/30">
                  {@html navigationIcons.chevronRight}
                </span>
              {/if}
            </button>
          {/each}
        </div>
      </div>
    {/each}
  {/if}
</div>
