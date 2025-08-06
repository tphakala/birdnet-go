<!--
  FileBrowser Component

  A secure file browser component using SVAR File Manager for Svelte 5.
  This component provides a directory browser interface for selecting export paths
  and other file system operations with security validation on the backend.

  Props:
  - currentPath?: string - Initial directory path to browse (defaults to server working directory)
  - onPathSelected?: (path: string) => void - Callback when a directory/path is selected
  - className?: string - Additional CSS classes
  - directoryOnly?: boolean - Only allow directory selection (no files)
  - disabled?: boolean - Disable the file browser

  Security Features:
  - All paths are validated on the backend with comprehensive security checks
  - Path traversal attacks are prevented (no ../ paths allowed)
  - Access to system directories is restricted
  - CSRF token protection for all API calls

  @component
-->

<script lang="ts">
  // @ts-expect-error - wx-svelte-filemanager may not have full TypeScript declarations
  import { Filemanager } from 'wx-svelte-filemanager';
  import { onMount } from 'svelte';
  import { cn } from '$lib/utils/cn.js';
  import { loggers } from '$lib/utils/logger';
  import type { Snippet } from 'svelte';

  const logger = loggers.ui;

  // Component Props Interface
  interface Props {
    currentPath?: string;
    onPathSelected?: (_path: string) => void;
    className?: string;
    directoryOnly?: boolean;
    disabled?: boolean;
    children?: Snippet;
  }

  // Destructure props with defaults
  let {
    currentPath = '',
    onPathSelected = () => {},
    className = '',
    directoryOnly = false,
    disabled = false,
    children,
    ...rest
  }: Props = $props();

  // File system item interface (matches backend API)
  interface FileSystemItem {
    id: string; // Full path
    size: number; // Size in bytes
    date: Date; // Modification date
    type: string; // "file" or "folder"
    name: string; // Display name
  }

  // State management
  let loading = $state(false);
  let error = $state<string | null>(null);
  let fileData = $state<FileSystemItem[]>([]);
  let browsePath = $state(currentPath);

  // CSRF token for API security
  let csrfToken = $derived(
    (document.querySelector('meta[name="csrf-token"]') as HTMLElement)?.getAttribute('content') ||
      ''
  );

  // Drive info (required by SVAR File Manager but not crucial for our use case)
  let driveInfo = $state({
    used: 0,
    total: 100 * 1024 * 1024 * 1024, // 100GB placeholder
  });

  /**
   * Fetch directory contents from backend API
   * @param path - Directory path to browse
   */
  async function fetchDirectoryContents(path: string = '') {
    loading = true;
    error = null;

    try {
      const params = new URLSearchParams();
      if (path) {
        params.set('path', path);
      }

      const response = await fetch(`/api/v2/filesystem/browse?${params}`, {
        headers: {
          'X-CSRF-Token': csrfToken,
          'Content-Type': 'application/json',
        },
        credentials: 'same-origin',
      });

      if (!response.ok) {
        const errorText = await response.text();
        throw new Error(`Server error ${response.status}: ${errorText}`);
      }

      const data = await response.json();

      // Convert backend response to SVAR File Manager format
      fileData = data.items.map((item: any) => ({
        id: item.id,
        size: item.size || 0,
        date: new Date(item.date),
        type: item.type === 'folder' ? 'folder' : 'file',
        name: item.name,
      }));

      // Filter to directories only if requested
      if (directoryOnly) {
        fileData = fileData.filter(item => item.type === 'folder');
      }

      // Update current browse path
      browsePath = data.currentPath || path;

      logger.info('Directory contents loaded', {
        path: browsePath,
        itemCount: fileData.length,
      });
    } catch (err) {
      const errorMessage = err instanceof Error ? err.message : 'Unknown error occurred';
      error = `Failed to load directory: ${errorMessage}`;
      fileData = [];

      logger.error('Failed to fetch directory contents', {
        path: path || browsePath,
        error: errorMessage,
      });
    } finally {
      loading = false;
    }
  }

  /**
   * Handle directory/file selection
   * @param item - Selected file system item
   */
  function handleSelection(item: FileSystemItem) {
    if (disabled) return;

    if (item.type === 'folder') {
      // Navigate to folder
      fetchDirectoryContents(item.id);
    } else if (!directoryOnly) {
      // Select file (if allowed)
      onPathSelected(item.id);
    }
  }

  /**
   * Handle navigation (e.g., back button, breadcrumb navigation)
   * @param path - Path to navigate to
   */
  function handleNavigation(path: string) {
    if (disabled) return;
    fetchDirectoryContents(path);
  }

  // Load initial directory on component mount
  onMount(() => {
    fetchDirectoryContents(currentPath);
  });

  // Watch for external path changes
  $effect(() => {
    if (currentPath !== browsePath) {
      fetchDirectoryContents(currentPath);
    }
  });
</script>

<div class={cn('file-browser-container', className)} {...rest}>
  {#if error}
    <!-- Error State -->
    <div class="bg-error/10 border border-error text-error-content rounded-lg p-4 mb-4">
      <div class="flex items-center gap-2">
        <svg class="w-5 h-5 fill-current" viewBox="0 0 20 20">
          <path
            d="M10 18a8 8 0 100-16 8 8 0 000 16zM8.707 7.293a1 1 0 00-1.414 1.414L8.586 10l-1.293 1.293a1 1 0 101.414 1.414L10 11.414l1.293 1.293a1 1 0 001.414-1.414L11.414 10l1.293-1.293a1 1 0 00-1.414-1.414L10 8.586 8.707 7.293z"
          />
        </svg>
        <span class="font-medium">Error</span>
      </div>
      <p class="mt-2 text-sm">{error}</p>
      <button
        type="button"
        class="btn btn-sm btn-error mt-3"
        onclick={() => fetchDirectoryContents(browsePath)}
      >
        Retry
      </button>
    </div>
  {:else if loading}
    <!-- Loading State -->
    <div class="flex items-center justify-center p-8">
      <div class="loading loading-spinner loading-lg"></div>
      <span class="ml-3 text-base-content/70">Loading directory...</span>
    </div>
  {:else}
    <!-- File Browser -->
    <div class="file-browser-wrapper" class:opacity-50={disabled}>
      <!-- Current Path Display -->
      <div class="bg-base-200 border border-base-300 rounded-t-lg px-4 py-2 text-sm font-mono">
        <span class="text-base-content/70">Current path:</span>
        <span class="text-base-content font-medium">{browsePath || '/'}</span>
      </div>

      <!-- SVAR File Manager -->
      <div class="border border-base-300 border-t-0 rounded-b-lg overflow-hidden">
        <Filemanager
          data={fileData}
          drive={driveInfo}
          on:select={(event: any) => handleSelection(event.detail)}
          on:navigate={(event: any) => handleNavigation(event.detail.path)}
        />
      </div>

      <!-- Directory Selection Actions (for directoryOnly mode) -->
      {#if directoryOnly}
        <div class="mt-4 flex justify-between items-center">
          <div class="text-sm text-base-content/70">Select a directory to continue</div>
          <div class="flex gap-2">
            <button
              type="button"
              class="btn btn-sm btn-outline"
              onclick={() => handleNavigation('..')}
              disabled={!browsePath || browsePath === '/'}
            >
              ← Back
            </button>
            <button
              type="button"
              class="btn btn-sm btn-primary"
              onclick={() => onPathSelected(browsePath)}
              disabled={!browsePath}
            >
              Select Current Directory
            </button>
          </div>
        </div>
      {/if}
    </div>
  {/if}

  <!-- Optional children content -->
  {#if children}
    <div class="mt-4">
      {@render children()}
    </div>
  {/if}
</div>

<style>
  /* Custom styles for the file browser */
  .file-browser-container {
    @apply relative;
  }

  .file-browser-wrapper :global(.wx-filemanager) {
    @apply border-0 bg-base-100;

    min-height: 400px;
    font-family: inherit;
  }

  /* Dark mode adjustments */
  .file-browser-wrapper :global(.wx-filemanager .wx-list-item:hover) {
    @apply bg-base-200;
  }

  .file-browser-wrapper :global(.wx-filemanager .wx-list-item.wx-selected) {
    @apply bg-primary/20;
  }
</style>
