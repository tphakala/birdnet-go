<script lang="ts">
  import { onDestroy } from 'svelte';
  import { loggers } from '$lib/utils/logger';
  import { validateProtocolURL } from '$lib/utils/security';

  const logger = loggers.ui;

  interface Props {
    urls: string[]; // Changed from RTSPUrl[] to string[] to match backend
    onUpdate: (_urls: string[]) => void;
    disabled?: boolean;
  }

  let { urls = [], onUpdate, disabled = false }: Props = $props();

  let newUrl = $state('');

  function isValidRtspUrl(url: string): boolean {
    // Use security utility for safe URL validation
    // Support both rtsp:// and rtsps:// protocols
    return validateProtocolURL(url, ['rtsp', 'rtsps'], 2048);
  }

  // Redact credentials from URL for safe logging
  function redactUrlCredentials(url: string): string {
    try {
      const urlObj = new URL(url);
      if (urlObj.username || urlObj.password) {
        // Replace credentials with [REDACTED]
        return url.replace(/(rtsps?:\/\/)[^@]+(@)/, '$1[REDACTED]$2');
      }
      return url;
    } catch {
      // If URL parsing fails, redact anything that looks like credentials
      return url.replace(/(rtsps?:\/\/)[^@]+(@)/, '$1[REDACTED]$2');
    }
  }

  function addUrl() {
    const trimmedUrl = newUrl.trim();
    if (trimmedUrl && isValidRtspUrl(trimmedUrl)) {
      const updatedUrls = [...urls, trimmedUrl]; // Simple string array
      onUpdate(updatedUrls);
      newUrl = '';
    } else if (trimmedUrl && !isValidRtspUrl(trimmedUrl)) {
      // URL is not empty but invalid - could add user feedback here
      logger.error('Invalid RTSP URL format', null, {
        url: redactUrlCredentials(trimmedUrl),
        component: 'RTSPUrlInput',
        action: 'addUrl',
      });
    }
  }

  function removeUrl(index: number) {
    const updatedUrls = urls.filter((_, i) => i !== index);
    onUpdate(updatedUrls);
  }

  // Debounce timers for each URL input to prevent excessive updates
  let updateTimers = $state<Record<number, ReturnType<typeof setTimeout>>>({});

  function updateUrl(index: number, value: string) {
    // Clear existing timer for this index
    // eslint-disable-next-line security/detect-object-injection -- Safe: index is validated number
    if (updateTimers[index]) {
      // eslint-disable-next-line security/detect-object-injection -- Safe: index is validated number
      clearTimeout(updateTimers[index]);
    }

    // Set debounced update with validation
    // eslint-disable-next-line security/detect-object-injection -- Safe: index is validated number
    updateTimers[index] = setTimeout(() => {
      // Only update if URL is valid or empty (allow clearing)
      if (value.trim() === '' || isValidRtspUrl(value.trim())) {
        const updatedUrls = [...urls];
        if (index >= 0 && index < updatedUrls.length) {
          // eslint-disable-next-line security/detect-object-injection -- Safe: index is validated above
          updatedUrls[index] = value.trim();
          onUpdate(updatedUrls);
        }
      } else {
        logger.warn('Invalid RTSP URL not applied', {
          component: 'RTSPUrlInput',
          action: 'updateUrl',
          url: redactUrlCredentials(value),
          index,
        });
      }

      // Clean up timer reference
      // eslint-disable-next-line security/detect-object-injection -- Safe: index is controlled
      delete updateTimers[index];
    }, 300); // 300ms debounce delay
  }

  // Cleanup all outstanding debounce timers on component unmount
  onDestroy(() => {
    Object.values(updateTimers).forEach(timer => {
      if (timer) {
        clearTimeout(timer);
      }
    });
    updateTimers = {}; // Clear the timers object
  });

  function handleKeydown(event: KeyboardEvent) {
    if (event.key === 'Enter') {
      event.preventDefault();
      addUrl();
    }
  }
</script>

<div class="space-y-2">
  {#each urls as url, index}
    <div class="flex items-center gap-2">
      <input
        type="text"
        value={url}
        oninput={e => updateUrl(index, e.currentTarget.value)}
        class="input input-sm flex-1"
        placeholder="rtsp://user:password@example.com/stream"
        {disabled}
      />
      <button
        type="button"
        onclick={() => removeUrl(index)}
        class="btn btn-error btn-xs"
        aria-label="Remove URL {index + 1}"
        {disabled}
      >
        Remove
      </button>
    </div>
  {/each}

  <div class="flex items-center gap-2">
    <input
      type="text"
      bind:value={newUrl}
      onkeydown={handleKeydown}
      class="input input-sm flex-1"
      placeholder="Enter RTSP URL (rtsp://user:password@example.com/stream1)"
      {disabled}
    />
    <button
      type="button"
      onclick={addUrl}
      class="btn btn-primary btn-sm"
      disabled={disabled || !newUrl.trim()}
    >
      Add
    </button>
  </div>
</div>
