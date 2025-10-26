<script>
  import { onMount } from 'svelte';
  import { actionIcons, alertIconsSvg, systemIcons } from '$lib/utils/icons';
  import { t } from '$lib/i18n';
  import { safeGet, safeArrayAccess } from '$lib/utils/security';
  import { deduplicateNotifications, sanitizeNotificationMessage } from '$lib/utils/notifications';

  // SPINNER CONTROL: Set to false to disable loading spinners (reduces flickering)
  // Change back to true to re-enable spinners for testing
  const ENABLE_LOADING_SPINNERS = false;

  let notifications = $state([]);
  let loading = $state(false);
  let currentPage = $state(1);
  let totalPages = $state(1);
  let pageSize = 20;
  let hasUnread = $state(false);
  let pendingDeleteId = $state(null);
  let deleteModal = $state(null);

  let filters = $state({
    status: '',
    type: '',
    priority: '',
  });

  // Get CSRF token
  function getCSRFToken() {
    const metaTag = document.querySelector('meta[name="csrf-token"]');
    return metaTag ? metaTag.getAttribute('content') : '';
  }

  // Load notifications from API
  async function loadNotifications() {
    loading = true;
    try {
      const params = new URLSearchParams({
        limit: pageSize.toString(),
        offset: ((currentPage - 1) * pageSize).toString(),
      });

      // Add filters
      if (filters.status) params.append('status', filters.status);
      if (filters.type) params.append('type', filters.type);
      if (filters.priority) params.append('priority', filters.priority);

      const response = await fetch(`/api/v2/notifications?${params}`);
      if (response.ok) {
        const data = await response.json();
        // Apply deduplication to remove duplicate notifications
        const rawNotifications = data.notifications || [];
        notifications = deduplicateNotifications(rawNotifications, {
          excludeToasts: false, // Show all notifications in the full view
        });
        hasUnread = notifications.some(n => !n.read);

        // Calculate total pages
        if (data.total !== undefined) {
          totalPages = Math.ceil(data.total / pageSize) || 1;
        } else {
          totalPages = notifications.length < pageSize ? currentPage : currentPage + 1;
        }
      }
    } catch {
      // Handle error silently for now
    } finally {
      loading = false;
    }
  }

  // Apply filters
  function applyFilters() {
    currentPage = 1;
    loadNotifications();
  }

  // Mark notification as read
  async function markAsRead(id, event) {
    if (event) {
      event.stopPropagation();
    }
    try {
      const response = await fetch(`/api/v2/notifications/${id}/read`, {
        method: 'PUT',
        headers: {
          'X-CSRF-Token': getCSRFToken(),
        },
      });

      if (response.ok) {
        const notification = notifications.find(n => n.id === id);
        if (notification) {
          notification.read = true;
          notification.status = 'read';
          hasUnread = notifications.some(n => !n.read);
        }
      }
    } catch {
      // Handle error silently for now
    }
  }

  // Handle notification click
  async function handleNotificationClick(notification) {
    // For detection notifications with note_id, navigate to detection detail page
    if (notification.type === 'detection' && notification.metadata?.note_id) {
      try {
        await markAsRead(notification.id);
      } catch {
        // Silently handle mark as read failures
      }
      window.location.href = `/ui/detections/${notification.metadata.note_id}`;
    }
  }

  // Mark all as read
  async function markAllAsRead() {
    const unreadIds = notifications.filter(n => !n.read).map(n => n.id);
    await Promise.all(unreadIds.map(id => markAsRead(id)));
  }

  // Acknowledge notification
  async function acknowledge(id, event) {
    if (event) {
      event.stopPropagation();
    }
    try {
      const response = await fetch(`/api/v2/notifications/${id}/acknowledge`, {
        method: 'PUT',
        headers: {
          'X-CSRF-Token': getCSRFToken(),
        },
      });

      if (response.ok) {
        const notification = notifications.find(n => n.id === id);
        if (notification) {
          notification.status = 'acknowledged';
        }
      }
    } catch {
      // Handle error silently for now
    }
  }

  // Delete notification
  async function deleteNotification(id, event) {
    if (event) {
      event.stopPropagation();
    }
    pendingDeleteId = id;
    deleteModal?.showModal();
  }

  // Confirm delete
  async function confirmDelete() {
    if (!pendingDeleteId) return;

    const id = pendingDeleteId;
    pendingDeleteId = null;

    try {
      const response = await fetch(`/api/v2/notifications/${id}`, {
        method: 'DELETE',
        headers: {
          'X-CSRF-Token': getCSRFToken(),
        },
      });

      if (response.ok) {
        deleteModal?.close();

        const index = notifications.findIndex(n => n.id === id);
        if (index !== -1) {
          const notification = safeArrayAccess(notifications, index);
          const wasUnread = notification ? !notification.read : false;
          notifications.splice(index, 1);
          hasUnread = notifications.some(n => !n.read);

          // Dispatch event for notification bell update
          window.dispatchEvent(
            new CustomEvent('notification-deleted', {
              detail: { id, wasUnread },
            })
          );

          // If page is empty, go to previous page
          if (notifications.length === 0 && currentPage > 1) {
            currentPage--;
            await loadNotifications();
          }
        }
      } else {
        deleteModal?.close();
        const errorData = await response.json().catch(() => ({}));
        alert(errorData.error || 'Failed to delete notification. Please try again.');
      }
    } catch {
      deleteModal?.close();
      // Handle error silently for now
      alert('Network error occurred. Please try again.');
    }
  }

  // Pagination
  function previousPage() {
    if (currentPage > 1) {
      currentPage--;
      loadNotifications();
    }
  }

  function nextPage() {
    if (currentPage < totalPages) {
      currentPage++;
      loadNotifications();
    }
  }

  // Get notification icon class
  function getNotificationIconClass(notification) {
    const baseClass = 'w-10 h-10 rounded-full flex items-center justify-center';
    const typeClasses = {
      error: 'bg-error/20 text-error',
      warning: 'bg-warning/20 text-warning',
      info: 'bg-info/20 text-info',
      detection: 'bg-success/20 text-success',
      system: 'bg-primary/20 text-primary',
    };
    return `${baseClass} ${safeGet(typeClasses, notification.type, 'bg-base-300')}`;
  }

  // Get notification card class
  function getNotificationCardClass(notification) {
    let classes = 'card bg-base-100 shadow-sm hover:shadow-md transition-shadow';
    if (!notification.read) {
      classes += ' bg-base-200/30';
    }
    if (notification.type === 'detection' && notification.metadata?.note_id) {
      classes += ' cursor-pointer';
    }
    return classes;
  }

  // Check if notification is clickable
  function isClickable(notification) {
    return notification.type === 'detection' && notification.metadata?.note_id;
  }

  // Get priority badge class
  function getPriorityBadgeClass(priority) {
    const classes = {
      critical: 'badge-error',
      high: 'badge-warning',
      medium: 'badge-info',
      low: 'badge-ghost',
    };
    return safeGet(classes, priority, 'badge-ghost');
  }

  // Format timestamp
  function formatTime(timestamp) {
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

  onMount(() => {
    loadNotifications();
  });
</script>

<div class="col-span-12 p-4">
  <!-- Filters and Actions -->
  <div class="card bg-base-100 shadow-sm mb-6">
    <div class="card-body">
      <div class="flex flex-wrap gap-4 items-center justify-between">
        <!-- Filters -->
        <div class="flex flex-wrap gap-2">
          <select
            bind:value={filters.status}
            onchange={applyFilters}
            class="select select-sm select-bordered"
            aria-label={t('notifications.aria.filterByStatus')}
          >
            <option value="">{t('notifications.filters.allStatus')}</option>
            <option value="unread">{t('notifications.filters.unread')}</option>
            <option value="read">{t('notifications.filters.read')}</option>
            <option value="acknowledged">{t('notifications.filters.acknowledged')}</option>
          </select>

          <select
            bind:value={filters.type}
            onchange={applyFilters}
            class="select select-sm select-bordered"
            aria-label={t('notifications.aria.filterByType')}
          >
            <option value="">{t('notifications.filters.allTypes')}</option>
            <option value="error">{t('notifications.filters.errors')}</option>
            <option value="warning">{t('notifications.filters.warnings')}</option>
            <option value="info">{t('notifications.filters.info')}</option>
            <option value="system">{t('notifications.filters.system')}</option>
            <option value="detection">{t('notifications.filters.detections')}</option>
          </select>

          <select
            bind:value={filters.priority}
            onchange={applyFilters}
            class="select select-sm select-bordered"
            aria-label={t('notifications.aria.filterByPriority')}
          >
            <option value="">{t('notifications.filters.allPriorities')}</option>
            <option value="critical">{t('notifications.filters.critical')}</option>
            <option value="high">{t('notifications.filters.high')}</option>
            <option value="medium">{t('notifications.filters.medium')}</option>
            <option value="low">{t('notifications.filters.low')}</option>
          </select>
        </div>

        <!-- Actions -->
        <div class="flex gap-2">
          {#if hasUnread}
            <button onclick={markAllAsRead} class="btn btn-sm btn-ghost"> Mark All Read </button>
          {/if}
          <button
            onclick={loadNotifications}
            class="btn btn-sm btn-ghost"
            aria-label={t('notifications.actions.refresh')}
          >
            {@html actionIcons.refresh}
            Refresh
          </button>
        </div>
      </div>
    </div>
  </div>

  <!-- Notifications List -->
  <div class="space-y-4" role="region" aria-label="Notifications list">
    {#if ENABLE_LOADING_SPINNERS && loading}
      <div class="card bg-base-100 shadow-sm">
        <div class="card-body">
          <div class="flex justify-center">
            <div class="loading loading-spinner loading-lg"></div>
          </div>
        </div>
      </div>
    {:else if notifications.length === 0}
      <div class="card bg-base-100 shadow-sm">
        <div class="card-body text-center py-12">
          <span class="opacity-30 mb-4" aria-hidden="true">
            {@html systemIcons.bellOff}
          </span>
          <p class="text-lg text-base-content/60">{t('notifications.empty.title')}</p>
          <p class="text-sm text-base-content/40">{t('notifications.empty.subtitle')}</p>
        </div>
      </div>
    {:else}
      {#each notifications as notification (notification.id)}
        <!-- Tabindex is conditionally added only when role="link" is set for clickable notifications -->
        <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
        <div
          class={getNotificationCardClass(notification)}
          onclick={() => handleNotificationClick(notification)}
          role={isClickable(notification) ? 'link' : undefined}
          tabindex={isClickable(notification) ? 0 : undefined}
          onkeydown={e => {
            if (
              isClickable(notification) &&
              e.currentTarget === e.target &&
              (e.key === 'Enter' || e.key === ' ' || e.key === 'Spacebar')
            ) {
              e.preventDefault();
              handleNotificationClick(notification);
            }
          }}
        >
          <div class="card-body">
            <div class="flex items-start gap-4">
              <!-- Icon -->
              <div class="flex-shrink-0">
                <div class={getNotificationIconClass(notification)}>
                  {#if notification.type === 'error'}
                    {@html alertIconsSvg.error}
                  {:else if notification.type === 'warning'}
                    {@html alertIconsSvg.warning}
                  {:else if notification.type === 'info'}
                    {@html alertIconsSvg.info}
                  {:else if notification.type === 'detection'}
                    {@html systemIcons.star}
                  {:else}
                    {@html systemIcons.settingsGear}
                  {/if}
                </div>
              </div>

              <!-- Content -->
              <div class="flex-1">
                <div class="flex items-start justify-between gap-4">
                  <div>
                    <h3 class="font-semibold text-lg">{notification.title}</h3>
                    <p class="text-base-content/80 mt-1">
                      {sanitizeNotificationMessage(notification.message)}
                    </p>

                    <!-- Metadata -->
                    <div class="flex flex-wrap items-center gap-2 mt-3">
                      {#if notification.component}
                        <span class="badge badge-ghost badge-sm">{notification.component}</span>
                      {/if}
                      <span class="badge badge-sm {getPriorityBadgeClass(notification.priority)}">
                        {notification.priority}
                      </span>
                      <time class="text-xs text-base-content/60" datetime={notification.timestamp}>
                        {formatTime(notification.timestamp)}
                      </time>
                    </div>
                  </div>

                  <!-- Actions -->
                  <div class="flex items-center gap-2">
                    {#if !notification.read}
                      <button
                        onclick={e => markAsRead(notification.id, e)}
                        class="btn btn-ghost btn-xs"
                        aria-label={t('notifications.actions.markAsRead')}
                      >
                        {@html systemIcons.eye}
                      </button>
                    {/if}
                    {#if notification.read && notification.status !== 'acknowledged'}
                      <button
                        onclick={e => acknowledge(notification.id, e)}
                        class="btn btn-ghost btn-xs"
                        aria-label={t('notifications.actions.acknowledge')}
                      >
                        {@html actionIcons.check}
                      </button>
                    {/if}
                    <button
                      onclick={e => deleteNotification(notification.id, e)}
                      class="btn btn-ghost btn-xs text-error"
                      aria-label={t('notifications.actions.delete')}
                    >
                      {@html actionIcons.delete}
                    </button>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      {/each}
    {/if}

    <!-- Pagination -->
    {#if !loading && totalPages > 1}
      <div class="flex justify-center mt-6" aria-label="Pagination">
        <div class="join">
          <button
            onclick={previousPage}
            disabled={currentPage === 1}
            class="join-item btn btn-sm"
            aria-label={t('dataDisplay.pagination.goToPreviousPage')}>«</button
          >
          <button
            class="join-item btn btn-sm btn-active"
            aria-label={t('dataDisplay.pagination.page', {
              current: currentPage,
              total: totalPages,
            })}
          >
            {t('dataDisplay.pagination.page', { current: currentPage, total: totalPages })}
          </button>
          <button
            onclick={nextPage}
            disabled={currentPage === totalPages}
            class="join-item btn btn-sm"
            aria-label={t('dataDisplay.pagination.goToNextPage')}>»</button
          >
        </div>
      </div>
    {/if}
  </div>

  <!-- Delete Confirmation Modal -->
  <dialog bind:this={deleteModal} class="modal">
    <div class="modal-box">
      <h3 class="font-bold text-lg">Confirm Delete</h3>
      <p class="py-4">Are you sure you want to delete this notification?</p>
      <div class="modal-action">
        <form method="dialog" class="flex gap-2">
          <button onclick={() => (pendingDeleteId = null)} class="btn btn-ghost">Cancel</button>
          <button type="button" onclick={confirmDelete} class="btn btn-error">Delete</button>
        </form>
      </div>
    </div>
    <form method="dialog" class="modal-backdrop">
      <button>close</button>
    </form>
  </dialog>
</div>
