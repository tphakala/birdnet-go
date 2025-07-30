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
    // RTSP URL validation regex pattern

    const rtspPattern =
      /^rtsp:\/\/(?:(?:[a-zA-Z0-9-._~!$&'()*+,;=:]|%[0-9A-Fa-f]{2})*@)?(?:\[(?:[0-9a-fA-F:.]+)\]|(?:[0-9]{1,3}\.){3}[0-9]{1,3}|(?:[a-zA-Z0-9-._~!$&'()*+,;=]|%[0-9A-Fa-f]{2})+)(?::[0-9]+)?(?:\/(?:[a-zA-Z0-9-._~!$&'()*+,;=:@]|%[0-9A-Fa-f]{2})*)*(?:\?(?:[a-zA-Z0-9-._~!$&'()*+,;=:@/?]|%[0-9A-Fa-f]{2})*)?(?:#(?:[a-zA-Z0-9-._~!$&'()*+,;=:@/?]|%[0-9A-Fa-f]{2})*)?$/i;
    return rtspPattern.test(url);
  }

  function addUrl() {
    const trimmedUrl = newUrl.trim();
    if (trimmedUrl && isValidRtspUrl(trimmedUrl)) {
      const updatedUrls = [...urls, { url: trimmedUrl, enabled: true }];
      onUpdate(updatedUrls);
      newUrl = '';
    } else if (trimmedUrl && !isValidRtspUrl(trimmedUrl)) {
      // URL is not empty but invalid - could add user feedback here
      // eslint-disable-next-line no-console
      console.warn('Invalid RTSP URL format:', trimmedUrl);
    }
  }

  function removeUrl(index: number) {
    const updatedUrls = urls.filter((_, i) => i !== index);
    onUpdate(updatedUrls);
  }

  function updateUrl(index: number, value: string) {
    const updatedUrls = [...urls];
    // eslint-disable-next-line security/detect-object-injection
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
