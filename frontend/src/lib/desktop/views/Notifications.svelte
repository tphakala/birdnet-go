<script>
  import { onMount } from 'svelte';
  import { actionIcons, alertIconsSvg, systemIcons } from '$lib/utils/icons';

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
        notifications = data.notifications || [];
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
  async function markAsRead(id) {
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

  // Mark all as read
  async function markAllAsRead() {
    const unreadIds = notifications.filter(n => !n.read).map(n => n.id);
    await Promise.all(unreadIds.map(id => markAsRead(id)));
  }

  // Acknowledge notification
  async function acknowledge(id) {
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
  async function deleteNotification(id) {
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
          const wasUnread = !notifications[index].read;
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
    return `${baseClass} ${typeClasses[notification.type] || 'bg-base-300'}`;
  }

  // Get priority badge class
  function getPriorityBadgeClass(priority) {
    const classes = {
      critical: 'badge-error',
      high: 'badge-warning',
      medium: 'badge-info',
      low: 'badge-ghost',
    };
    return classes[priority] || 'badge-ghost';
  }

  // Format timestamp
  function formatTime(timestamp) {
    const date = new Date(timestamp);
    const now = new Date();
    const diff = now.getTime() - date.getTime();

    if (diff < 60000) return 'Just now';
    if (diff < 3600000) return `${Math.floor(diff / 60000)}m ago`;
    if (diff < 86400000) return `${Math.floor(diff / 3600000)}h ago`;
    if (diff < 604800000) return `${Math.floor(diff / 86400000)}d ago`;

    return date.toLocaleDateString();
  }

  onMount(() => {
    loadNotifications();
  });
</script>

<div class="col-span-12 p-4">
  <!-- Page Header -->
  <div class="mb-4">
    <h1 class="text-3xl font-bold mb-2">Notifications</h1>
    <p class="text-base-content/70">View and manage all system notifications</p>
  </div>

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
            aria-label="Filter by status"
          >
            <option value="">All Status</option>
            <option value="unread">Unread</option>
            <option value="read">Read</option>
            <option value="acknowledged">Acknowledged</option>
          </select>

          <select
            bind:value={filters.type}
            onchange={applyFilters}
            class="select select-sm select-bordered"
            aria-label="Filter by type"
          >
            <option value="">All Types</option>
            <option value="error">Errors</option>
            <option value="warning">Warnings</option>
            <option value="info">Info</option>
            <option value="system">System</option>
            <option value="detection">Detections</option>
          </select>

          <select
            bind:value={filters.priority}
            onchange={applyFilters}
            class="select select-sm select-bordered"
            aria-label="Filter by priority"
          >
            <option value="">All Priorities</option>
            <option value="critical">Critical</option>
            <option value="high">High</option>
            <option value="medium">Medium</option>
            <option value="low">Low</option>
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
            aria-label="Refresh notifications"
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
    {#if loading}
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
          <p class="text-lg text-base-content/60">No notifications found</p>
          <p class="text-sm text-base-content/40">Adjust your filters or check back later</p>
        </div>
      </div>
    {:else}
      {#each notifications as notification (notification.id)}
        <article
          class="card bg-base-100 shadow-sm hover:shadow-md transition-shadow {!notification.read
            ? 'bg-base-200/30'
            : ''}"
        >
          <div class="card-body">
            <div class="flex items-start gap-4">
              <!-- Icon -->
              <div class="flex-shrink-0">
                <div class={getNotificationIconClass(notification)}>
                  {#if notification.type === 'error'}
                    {@html alertIconsSvg.error}
                  {:else if notification.type === 'warning'}
                    <svg
                      xmlns="http://www.w3.org/2000/svg"
                      fill="none"
                      viewBox="0 0 24 24"
                      stroke-width="2"
                      stroke="currentColor"
                      class="w-6 h-6"
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
                      class="w-6 h-6"
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
                      class="w-6 h-6"
                    >
                      <path
                        stroke-linecap="round"
                        stroke-linejoin="round"
                        d="M11.48 3.499a.562.562 0 011.04 0l2.125 5.111a.563.563 0 00.475.345l5.518.442c.499.04.701.663.321.988l-4.204 3.602a.563.563 0 00-.182.557l1.285 5.385a.562.562 0 01-.84.61l-4.725-2.885a.563.563 0 00-.586 0L6.982 20.54a.562.562 0 01-.84-.61l1.285-5.386a.562.562 0 00-.182-.557l-4.204-3.602a.563.563 0 01.321-.988l5.518-.442a.563.563 0 00.475-.345L11.48 3.5z"
                      />
                    </svg>
                  {:else}
                    <svg
                      xmlns="http://www.w3.org/2000/svg"
                      fill="none"
                      viewBox="0 0 24 24"
                      stroke-width="2"
                      stroke="currentColor"
                      class="w-6 h-6"
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

              <!-- Content -->
              <div class="flex-1">
                <div class="flex items-start justify-between gap-4">
                  <div>
                    <h3 class="font-semibold text-lg">{notification.title}</h3>
                    <p class="text-base-content/80 mt-1">{notification.message}</p>

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
                        onclick={() => markAsRead(notification.id)}
                        class="btn btn-ghost btn-xs"
                        aria-label="Mark as read"
                      >
                        <svg
                          xmlns="http://www.w3.org/2000/svg"
                          fill="none"
                          viewBox="0 0 24 24"
                          stroke-width="1.5"
                          stroke="currentColor"
                          class="w-4 h-4"
                        >
                          <path
                            stroke-linecap="round"
                            stroke-linejoin="round"
                            d="M2.036 12.322a1.012 1.012 0 010-.639C3.423 7.51 7.36 4.5 12 4.5c4.638 0 8.573 3.007 9.963 7.178.07.207.07.431 0 .639C20.577 16.49 16.64 19.5 12 19.5c-4.638 0-8.573-3.007-9.963-7.178z"
                          />
                          <path
                            stroke-linecap="round"
                            stroke-linejoin="round"
                            d="M15 12a3 3 0 11-6 0 3 3 0 016 0z"
                          />
                        </svg>
                      </button>
                    {/if}
                    {#if notification.read && notification.status !== 'acknowledged'}
                      <button
                        onclick={() => acknowledge(notification.id)}
                        class="btn btn-ghost btn-xs"
                        aria-label="Acknowledge"
                      >
                        <svg
                          xmlns="http://www.w3.org/2000/svg"
                          fill="none"
                          viewBox="0 0 24 24"
                          stroke-width="1.5"
                          stroke="currentColor"
                          class="w-4 h-4"
                        >
                          <path
                            stroke-linecap="round"
                            stroke-linejoin="round"
                            d="M4.5 12.75l6 6 9-13.5"
                          />
                        </svg>
                      </button>
                    {/if}
                    <button
                      onclick={() => deleteNotification(notification.id)}
                      class="btn btn-ghost btn-xs text-error"
                      aria-label="Delete"
                    >
                      <svg
                        xmlns="http://www.w3.org/2000/svg"
                        fill="none"
                        viewBox="0 0 24 24"
                        stroke-width="1.5"
                        stroke="currentColor"
                        class="w-4 h-4"
                      >
                        <path
                          stroke-linecap="round"
                          stroke-linejoin="round"
                          d="M14.74 9l-.346 9m-4.788 0L9.26 9m9.968-3.21c.342.052.682.107 1.022.166m-1.022-.165L18.16 19.673a2.25 2.25 0 01-2.244 2.077H8.084a2.25 2.25 0 01-2.244-2.077L4.772 5.79m14.456 0a48.108 48.108 0 00-3.478-.397m-12 .562c.34-.059.68-.114 1.022-.165m0 0a48.11 48.11 0 013.478-.397m7.5 0v-.916c0-1.18-.91-2.164-2.09-2.201a51.964 51.964 0 00-3.32 0c-1.18.037-2.09 1.022-2.09 2.201v.916m7.5 0a48.667 48.667 0 00-7.5 0"
                        />
                      </svg>
                    </button>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </article>
      {/each}
    {/if}

    <!-- Pagination -->
    {#if !loading && totalPages > 1}
      <div class="flex justify-center mt-6" aria-label="Pagination">
        <div class="join">
          <button onclick={previousPage} disabled={currentPage === 1} class="join-item btn btn-sm"
            >«</button
          >
          <button class="join-item btn btn-sm btn-active">
            Page {currentPage} of {totalPages}
          </button>
          <button
            onclick={nextPage}
            disabled={currentPage === totalPages}
            class="join-item btn btn-sm">»</button
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
