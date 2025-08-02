<script lang="ts">
  import type { RTSPUrl } from '$lib/stores/settings';
  import { loggers } from '$lib/utils/logger';
  import { validateProtocolURL } from '$lib/utils/security';

  const logger = loggers.ui;

  interface Props {
    urls: RTSPUrl[];
    onUpdate: (_urls: RTSPUrl[]) => void;
    disabled?: boolean;
  }

  let { urls = [], onUpdate, disabled = false }: Props = $props();

  let newUrl = $state('');

  function isValidRtspUrl(url: string): boolean {
    // Use security utility for safe URL validation
    return validateProtocolURL(url, ['rtsp'], 2048);
  }

  // Redact credentials from URL for safe logging
  function redactUrlCredentials(url: string): string {
    try {
      const urlObj = new URL(url);
      if (urlObj.username || urlObj.password) {
        // Replace credentials with [REDACTED]
        return url.replace(/(rtsp:\/\/)[^@]+(@)/, '$1[REDACTED]$2');
      }
      return url;
    } catch {
      // If URL parsing fails, redact anything that looks like credentials
      return url.replace(/(rtsp:\/\/)[^@]+(@)/, '$1[REDACTED]$2');
    }
  }

  function addUrl() {
    const trimmedUrl = newUrl.trim();
    if (trimmedUrl && isValidRtspUrl(trimmedUrl)) {
      const updatedUrls = [...urls, { url: trimmedUrl, enabled: true }];
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

  function updateUrl(index: number, value: string) {
    const updatedUrls = [...urls];
    updatedUrls[index] = { ...updatedUrls[index], url: value };
    onUpdate(updatedUrls);
  }

  function handleKeypress(event: KeyboardEvent) {
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
        value={url.url}
        oninput={e => updateUrl(index, e.currentTarget.value)}
        class="input input-bordered input-sm flex-1"
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
      onkeypress={handleKeypress}
      class="input input-bordered input-sm flex-1"
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
