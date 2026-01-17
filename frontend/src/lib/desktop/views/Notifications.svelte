<script>
  import { onMount } from 'svelte';
  import {
    RefreshCw,
    Trash2,
    Check,
    Eye,
    BellOff,
    XCircle,
    TriangleAlert,
    Info,
    Star,
    Settings,
    Layers,
    List,
    Filter,
  } from '@lucide/svelte';
  import { cn } from '$lib/utils/cn';
  import { t } from '$lib/i18n';
  import { safeGet, safeArrayAccess } from '$lib/utils/security';
  import {
    deduplicateNotifications,
    sanitizeNotificationMessage,
    groupNotifications,
    mapApiNotifications,
  } from '$lib/utils/notifications';
  import NotificationGroup from '$lib/desktop/components/ui/NotificationGroup.svelte';
  import SelectDropdown from '$lib/desktop/components/forms/SelectDropdown.svelte';
  import { toastActions } from '$lib/stores/toast';
  import { api } from '$lib/utils/api';
  import { buildAppUrl } from '$lib/utils/urlHelpers';

  // SPINNER CONTROL: Set to false to disable loading spinners (reduces flickering)
  // Change back to true to re-enable spinners for testing
  const ENABLE_LOADING_SPINNERS = false;

  // View mode: 'grouped' or 'flat' with localStorage persistence
  const STORAGE_KEY = 'notifications-view-mode';
  let viewMode = $state(
    (typeof localStorage !== 'undefined' && localStorage.getItem(STORAGE_KEY)) || 'grouped'
  );

  // Persist view mode changes to localStorage
  $effect(() => {
    if (typeof localStorage !== 'undefined') {
      localStorage.setItem(STORAGE_KEY, viewMode);
    }
  });

  let notifications = $state([]);
  let loading = $state(false);
  let currentPage = $state(1);
  let totalPages = $state(1);
  let pageSize = 20;
  let hasUnread = $state(false);
  let pendingDeleteId = $state(null);
  let pendingBulkDeleteIds = $state(null);
  // Element bindings should NOT use $state - causes showModal() to fail
  /** @type {HTMLDialogElement | null} */
  let deleteModal = null;
  /** @type {HTMLDialogElement | null} */
  let bulkDeleteModal = null;

  let filters = $state({
    status: '',
    type: '',
    priority: '',
  });

  // Filter options for SelectDropdown components
  let statusOptions = $derived([
    { value: '', label: t('notifications.filters.allStatus') },
    { value: 'unread', label: t('notifications.filters.unread') },
    { value: 'read', label: t('notifications.filters.read') },
    { value: 'acknowledged', label: t('notifications.filters.acknowledged') },
  ]);

  let typeOptions = $derived([
    { value: '', label: t('notifications.filters.allTypes') },
    { value: 'error', label: t('notifications.filters.errors'), icon: XCircle },
    { value: 'warning', label: t('notifications.filters.warnings'), icon: TriangleAlert },
    { value: 'info', label: t('notifications.filters.info'), icon: Info },
    { value: 'system', label: t('notifications.filters.system'), icon: Settings },
    { value: 'detection', label: t('notifications.filters.detections'), icon: Star },
  ]);

  let priorityOptions = $derived([
    { value: '', label: t('notifications.filters.allPriorities') },
    { value: 'critical', label: t('notifications.filters.critical') },
    { value: 'high', label: t('notifications.filters.high') },
    { value: 'medium', label: t('notifications.filters.medium') },
    { value: 'low', label: t('notifications.filters.low') },
  ]);

  // Grouped notifications derived from notifications array
  let groupedNotifications = $derived.by(() => {
    if (viewMode !== 'grouped' || notifications.length === 0) return [];
    return groupNotifications(notifications);
  });

  // Load notifications from API
  async function loadNotifications() {
    loading = true;
    try {
      // Use larger limit for grouped view to capture more notification types
      const effectiveLimit = viewMode === 'grouped' ? 100 : pageSize;
      const effectiveOffset = viewMode === 'grouped' ? 0 : (currentPage - 1) * pageSize;

      const params = new URLSearchParams({
        limit: effectiveLimit.toString(),
        offset: effectiveOffset.toString(),
      });

      // Add filters
      if (filters.status) params.append('status', filters.status);
      if (filters.type) params.append('type', filters.type);
      if (filters.priority) params.append('priority', filters.priority);

      const data = await api.get(`/api/v2/notifications?${params}`);
      // Map API notifications to frontend format (status -> read)
      // then apply deduplication to remove duplicate notifications
      const rawNotifications = data.notifications || [];
      const mappedNotifications = mapApiNotifications(rawNotifications);
      notifications = deduplicateNotifications(mappedNotifications, {
        excludeToasts: false, // Show all notifications in the full view
      });
      hasUnread = notifications.some(n => !n.read);

      // Calculate total pages using the raw total from the API for a stable page count.
      // Client-side deduplication may result in fewer items on some pages, but the
      // total remains consistent for better UX.
      if (data.total !== undefined) {
        totalPages = Math.ceil(data.total / pageSize) || 1;
      } else {
        // Fallback when total is not available
        totalPages = notifications.length < pageSize ? currentPage : currentPage + 1;
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
      await api.put(`/api/v2/notifications/${id}/read`);
      const notification = notifications.find(n => n.id === id);
      if (notification) {
        notification.read = true;
        notification.status = 'read';
        hasUnread = notifications.some(n => !n.read);
      }
    } catch {
      // Handle error silently for now
    }
  }

  // Handle notification click
  async function handleNotificationClick(notification) {
    // For detection notifications with note_id, navigate to detection detail page
    if (notification.type === 'detection' && notification.metadata?.note_id) {
      const noteId = notification.metadata.note_id;
      // Validate note_id is a positive integer
      if (typeof noteId === 'number' && Number.isInteger(noteId) && noteId > 0) {
        try {
          await markAsRead(notification.id);
        } catch {
          // Silently handle mark as read failures
        }
        window.location.href = buildAppUrl(`/ui/detections/${noteId}`);
      }
    }
  }

  // Mark all as read
  async function markAllAsRead() {
    const unreadIds = notifications.filter(n => !n.read).map(n => n.id);
    await Promise.all(unreadIds.map(id => markAsRead(id)));
  }

  // Mark multiple notifications as read (for group actions)
  async function handleMarkGroupAsRead(ids) {
    await Promise.all(ids.map(id => markAsRead(id)));
  }

  // Dismiss/delete multiple notifications (for group actions)
  function handleDismissGroup(ids) {
    pendingBulkDeleteIds = ids;
    bulkDeleteModal?.showModal();
  }

  // Confirm bulk delete
  async function confirmBulkDelete() {
    if (!pendingBulkDeleteIds || pendingBulkDeleteIds.length === 0) return;

    const ids = [...pendingBulkDeleteIds];
    pendingBulkDeleteIds = null;

    try {
      // Delete all notifications in parallel
      const results = await Promise.all(
        ids.map(async id => {
          try {
            await api.delete(`/api/v2/notifications/${id}`);
            return { id, ok: true };
          } catch {
            return { id, ok: false };
          }
        })
      );

      bulkDeleteModal?.close();

      // Remove successfully deleted notifications from local state
      const deletedIds = results.filter(r => r.ok).map(r => r.id);
      const wasUnreadCount = notifications.filter(n => deletedIds.includes(n.id) && !n.read).length;

      notifications = notifications.filter(n => !deletedIds.includes(n.id));
      hasUnread = notifications.some(n => !n.read);

      // Dispatch event for notification bell update
      if (deletedIds.length > 0) {
        window.dispatchEvent(
          new CustomEvent('notifications-bulk-deleted', {
            detail: { count: deletedIds.length, wasUnreadCount },
          })
        );
      }

      // If page is empty, go to previous page
      if (notifications.length === 0 && currentPage > 1) {
        currentPage--;
        await loadNotifications();
      }
    } catch {
      bulkDeleteModal?.close();
      toastActions.error(t('notifications.errors.networkError'));
    }
  }

  // Acknowledge notification
  async function acknowledge(id, event) {
    if (event) {
      event.stopPropagation();
    }
    try {
      await api.put(`/api/v2/notifications/${id}/acknowledge`);
      const notification = notifications.find(n => n.id === id);
      if (notification) {
        notification.status = 'acknowledged';
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
      await api.delete(`/api/v2/notifications/${id}`);
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
    } catch (error) {
      deleteModal?.close();
      const errorMessage =
        error instanceof Error ? error.message : t('notifications.errors.deleteFailed');
      toastActions.error(errorMessage);
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

  // Get notification icon class (compact for flat view)
  function getNotificationIconClass(notification) {
    const baseClass = 'w-8 h-8 rounded-full flex items-center justify-center';
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
    let classes =
      'card bg-base-100 shadow-2xs hover:shadow-md transition-shadow focus-visible:outline-solid focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary';
    if (!notification.read) {
      // Unread notifications get a subtle highlight, NOT opacity reduction
      classes += ' border-l-4 border-primary';
    } else {
      // Read notifications are slightly muted
      classes += ' opacity-70';
    }
    if (isClickable(notification)) {
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
  <!-- Filters and Actions Bar -->
  <div class="flex flex-wrap items-center justify-between gap-4 mb-6">
    <!-- Filters Group -->
    <div class="flex flex-wrap items-center gap-3">
      <span class="text-sm font-medium text-base-content/70 flex items-center gap-1.5">
        <Filter class="size-4" />
        {t('notifications.filters.label')}
      </span>

      <!-- Status Filter -->
      <div class="w-[140px]">
        <SelectDropdown
          options={statusOptions}
          bind:value={filters.status}
          placeholder={t('notifications.filters.allStatus')}
          size="sm"
          menuSize="sm"
          onChange={applyFilters}
        />
      </div>

      <!-- Type Filter -->
      <div class="w-[160px]">
        <SelectDropdown
          options={typeOptions}
          bind:value={filters.type}
          placeholder={t('notifications.filters.allTypes')}
          size="sm"
          menuSize="sm"
          onChange={applyFilters}
        />
      </div>

      <!-- Priority Filter -->
      <div class="w-[140px]">
        <SelectDropdown
          options={priorityOptions}
          bind:value={filters.priority}
          placeholder={t('notifications.filters.allPriorities')}
          size="sm"
          menuSize="sm"
          onChange={applyFilters}
        />
      </div>
    </div>

    <!-- View Toggle and Actions -->
    <div class="flex items-center gap-2">
      <!-- View Mode Toggle (DaisyUI tabs style) -->
      <div
        class="tabs tabs-boxed tabs-sm"
        role="tablist"
        aria-label={t('notifications.viewMode.label')}
      >
        <button
          onclick={() => (viewMode = 'grouped')}
          class={cn('tab gap-1.5', viewMode === 'grouped' && 'tab-active')}
          role="tab"
          aria-selected={viewMode === 'grouped'}
          aria-label={t('notifications.viewMode.grouped')}
        >
          <Layers class="size-4" />
          <span class="hidden sm:inline">{t('notifications.viewMode.grouped')}</span>
        </button>
        <button
          onclick={() => (viewMode = 'flat')}
          class={cn('tab gap-1.5', viewMode === 'flat' && 'tab-active')}
          role="tab"
          aria-selected={viewMode === 'flat'}
          aria-label={t('notifications.viewMode.flat')}
        >
          <List class="size-4" />
          <span class="hidden sm:inline">{t('notifications.viewMode.flat')}</span>
        </button>
      </div>

      <div class="divider divider-horizontal mx-0"></div>

      {#if hasUnread}
        <button onclick={markAllAsRead} class="btn btn-ghost btn-sm gap-1.5">
          <Eye class="size-4" />
          <span class="hidden sm:inline">{t('notifications.actions.markAllRead')}</span>
        </button>
      {/if}
      <button
        onclick={loadNotifications}
        class="btn btn-ghost btn-sm btn-square"
        aria-label={t('notifications.actions.refresh')}
      >
        <RefreshCw class="size-4" />
      </button>
    </div>
  </div>

  <!-- Notifications List -->
  <div class="space-y-3" role="region" aria-label="Notifications list">
    {#if ENABLE_LOADING_SPINNERS && loading}
      <div class="card bg-base-100 shadow-2xs">
        <div class="card-body">
          <div class="flex justify-center">
            <div class="loading loading-spinner loading-lg"></div>
          </div>
        </div>
      </div>
    {:else if notifications.length === 0}
      <div class="card bg-base-100 shadow-2xs">
        <div class="card-body text-center py-12">
          <span class="opacity-30 mb-4" aria-hidden="true">
            <BellOff class="size-12" />
          </span>
          <p class="text-sm text-base-content opacity-60">{t('notifications.empty.title')}</p>
          <p class="text-xs text-base-content opacity-40">{t('notifications.empty.subtitle')}</p>
        </div>
      </div>
    {:else if viewMode === 'grouped'}
      <!-- Grouped View -->
      {#each groupedNotifications as group (group.key)}
        <NotificationGroup
          {group}
          defaultOpen={group.notifications.length === 1}
          onMarkAllRead={handleMarkGroupAsRead}
          onDismissAll={handleDismissGroup}
          onMarkAsRead={id => markAsRead(id)}
          onAcknowledge={id => acknowledge(id)}
          onDelete={id => deleteNotification(id)}
          onNotificationClick={handleNotificationClick}
        />
      {/each}
    {:else}
      <!-- Flat View with compact styling -->
      {#each notifications as notification (notification.id)}
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
          <div class="card-body p-3">
            <div class="flex items-start gap-3">
              <!-- Icon (smaller) -->
              <div class="shrink-0">
                <div class={getNotificationIconClass(notification)}>
                  {#if notification.type === 'error'}
                    <XCircle class="size-4" />
                  {:else if notification.type === 'warning'}
                    <TriangleAlert class="size-4" />
                  {:else if notification.type === 'info'}
                    <Info class="size-4" />
                  {:else if notification.type === 'detection'}
                    <Star class="size-4" />
                  {:else}
                    <Settings class="size-4" />
                  {/if}
                </div>
              </div>

              <!-- Content (more compact) -->
              <div class="flex-1 min-w-0">
                <div class="flex items-start justify-between gap-2">
                  <div class="min-w-0">
                    <h3 class="font-medium text-sm truncate">{notification.title}</h3>
                    <p class="text-xs text-base-content/80 mt-0.5 line-clamp-2">
                      {sanitizeNotificationMessage(notification.message)}
                    </p>

                    <!-- Metadata (compact) -->
                    <div class="flex flex-wrap items-center gap-1.5 mt-2">
                      {#if notification.component}
                        <span class="badge badge-ghost badge-xs">{notification.component}</span>
                      {/if}
                      <span class="badge badge-xs {getPriorityBadgeClass(notification.priority)}">
                        {notification.priority}
                      </span>
                      <time class="text-xs text-base-content/50" datetime={notification.timestamp}>
                        {formatTime(notification.timestamp)}
                      </time>
                    </div>
                  </div>

                  <!-- Actions -->
                  <div class="flex items-center gap-1 shrink-0">
                    {#if !notification.read}
                      <button
                        onclick={e => markAsRead(notification.id, e)}
                        class="p-1 text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200 hover:bg-gray-100 dark:hover:bg-gray-700 rounded transition-colors"
                        aria-label={t('notifications.actions.markAsRead')}
                      >
                        <Eye class="size-3" />
                      </button>
                    {/if}
                    {#if notification.read && notification.status !== 'acknowledged'}
                      <button
                        onclick={e => acknowledge(notification.id, e)}
                        class="p-1 text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200 hover:bg-gray-100 dark:hover:bg-gray-700 rounded transition-colors"
                        aria-label={t('notifications.actions.acknowledge')}
                      >
                        <Check class="size-3" />
                      </button>
                    {/if}
                    <button
                      onclick={e => deleteNotification(notification.id, e)}
                      class="p-1 text-red-500 hover:text-red-700 hover:bg-red-50 dark:hover:bg-red-900/20 rounded transition-colors"
                      aria-label={t('notifications.actions.delete')}
                    >
                      <Trash2 class="size-3" />
                    </button>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      {/each}
    {/if}

    <!-- Pagination (only shown in flat view - grouped view shows all loaded notifications) -->
    {#if !loading && totalPages > 1 && viewMode === 'flat'}
      <div class="flex justify-center mt-6" aria-label="Pagination">
        <div class="join">
          <button
            onclick={previousPage}
            disabled={currentPage === 1}
            class="join-item btn btn-sm"
            aria-label={t('dataDisplay.pagination.goToPreviousPage')}>«</button
          >
          <button
            class="join-item btn btn-sm btn-primary"
            aria-label={t('dataDisplay.pagination.page', {
              current: currentPage,
              total: totalPages,
            })}
            aria-current="page"
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
  <dialog
    bind:this={deleteModal}
    class="fixed inset-0 z-50 m-auto max-w-sm w-full rounded-lg bg-white dark:bg-gray-800 shadow-xl backdrop:bg-black/50"
    onclose={() => (pendingDeleteId = null)}
    onclick={e => {
      if (e.currentTarget === e.target) {
        deleteModal?.close();
      }
    }}
  >
    <div class="p-6">
      <h3 class="font-bold text-lg text-gray-900 dark:text-gray-100">
        {t('notifications.actions.confirmDelete')}
      </h3>
      <p class="py-4 text-gray-600 dark:text-gray-300">
        {t('notifications.actions.deleteConfirmation')}
      </p>
      <div class="flex justify-end gap-2 mt-4">
        <button
          type="button"
          onclick={() => deleteModal?.close()}
          class="px-4 py-2 text-sm font-medium text-gray-700 dark:text-gray-300 bg-gray-100 dark:bg-gray-700 hover:bg-gray-200 dark:hover:bg-gray-600 rounded-lg transition-colors"
        >
          {t('common.cancel')}
        </button>
        <button
          type="button"
          onclick={confirmDelete}
          class="px-4 py-2 text-sm font-medium text-white bg-red-600 hover:bg-red-700 rounded-lg transition-colors"
        >
          {t('common.delete')}
        </button>
      </div>
    </div>
  </dialog>

  <!-- Bulk Delete Confirmation Modal -->
  <dialog
    bind:this={bulkDeleteModal}
    class="fixed inset-0 z-50 m-auto max-w-sm w-full rounded-lg bg-white dark:bg-gray-800 shadow-xl backdrop:bg-black/50"
    onclose={() => (pendingBulkDeleteIds = null)}
    onclick={e => {
      if (e.currentTarget === e.target) {
        bulkDeleteModal?.close();
      }
    }}
  >
    <div class="p-6">
      <h3 class="font-bold text-lg text-gray-900 dark:text-gray-100">
        {t('notifications.groups.confirmBulkDelete')}
      </h3>
      <p class="py-4 text-gray-600 dark:text-gray-300">
        {t('notifications.groups.bulkDeleteConfirmation', {
          count: pendingBulkDeleteIds?.length ?? 0,
        })}
      </p>
      <div class="flex justify-end gap-2 mt-4">
        <button
          type="button"
          onclick={() => bulkDeleteModal?.close()}
          class="px-4 py-2 text-sm font-medium text-gray-700 dark:text-gray-300 bg-gray-100 dark:bg-gray-700 hover:bg-gray-200 dark:hover:bg-gray-600 rounded-lg transition-colors"
        >
          {t('common.cancel')}
        </button>
        <button
          type="button"
          onclick={confirmBulkDelete}
          class="px-4 py-2 text-sm font-medium text-white bg-red-600 hover:bg-red-700 rounded-lg transition-colors"
        >
          {t('common.delete')}
        </button>
      </div>
    </div>
  </dialog>
</div>
