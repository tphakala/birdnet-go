<script lang="ts">
  import type { RTSPUrl } from '$lib/stores/settings';

  interface Props {
    urls: RTSPUrl[];
    onUpdate: (_urls: RTSPUrl[]) => void;
    disabled?: boolean;
  }

  let { urls = [], onUpdate, disabled = false }: Props = $props();

  let newUrl = $state('');

  function addUrl() {
    if (newUrl.trim()) {
      const updatedUrls = [...urls, { url: newUrl.trim(), enabled: true }];
      onUpdate(updatedUrls);
      newUrl = '';
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
