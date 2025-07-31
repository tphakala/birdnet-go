<script lang="ts">
  import type { RTSPUrl } from '$lib/stores/settings';

  interface Props {
    urls: RTSPUrl[];
    onUpdate: (_urls: RTSPUrl[]) => void;
    disabled?: boolean;
  }

  let { urls = [], onUpdate, disabled = false }: Props = $props();

  let newUrl = $state('');

  function isValidRtspUrl(url: string): boolean {
    // Safer RTSP URL validation - basic format check to prevent ReDoS
    // Check for basic RTSP URL structure without complex nested quantifiers
    if (!url.startsWith('rtsp://')) {
      return false;
    }

    try {
      // Use URL constructor for validation where possible
      const parsed = new URL(url);
      return parsed.protocol === 'rtsp:';
    } catch {
      // Fallback to simpler regex for RTSP-specific validation
      // This pattern is much simpler and safer from ReDoS attacks
      const safeRtspPattern = /^rtsp:\/\/[\w.-]+(?::[0-9]{1,5})?(?:\/[\w/.?&=%-]*)?$/i;
      return safeRtspPattern.test(url);
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
      console.error('Invalid RTSP URL format:', trimmedUrl); // TODO: Replace with Sentry.io logging
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
