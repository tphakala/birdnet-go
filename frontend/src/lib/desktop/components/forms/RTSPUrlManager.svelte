<script lang="ts">
  import { cn } from '$lib/utils/cn.js';
  import FormField from './FormField.svelte';
  import ToggleField from './ToggleField.svelte';
  import type { HTMLAttributes } from 'svelte/elements';
  import { Plus, X, Video, TriangleAlert } from '@lucide/svelte';
  import { createSafeMap } from '$lib/utils/security';

  interface RTSPUrl {
    id: string;
    url: string;
    name: string;
    active: boolean;
  }

  interface Props extends HTMLAttributes<HTMLDivElement> {
    label: string;
    urls: RTSPUrl[];
    onUpdate: (_urls: RTSPUrl[]) => void;
    helpText?: string;
    disabled?: boolean;
    error?: string;
    className?: string;
    maxItems?: number;
  }

  let {
    label,
    urls,
    onUpdate,
    helpText = '',
    disabled = false,
    error,
    className = '',
    maxItems = 5,
    ...rest
  }: Props = $props();

  let newUrl = $state('');
  let newName = $state('');
  let fieldId = `rtsp-${Math.random().toString(36).substring(2, 11)}`;
  let errors = $state(createSafeMap<string>());

  // Validation function for RTSP URLs
  function validateRTSPUrl(url: string): string | null {
    if (!url || url.trim().length === 0) {
      return 'RTSP URL cannot be empty';
    }

    const trimmed = url.trim();

    // Basic RTSP URL format check
    const rtspPattern = /^rtsp:\/\/.+/i;
    if (!rtspPattern.test(trimmed)) {
      return 'URL must start with rtsp://';
    }

    // More comprehensive URL validation
    try {
      const urlObj = new URL(trimmed);
      if (urlObj.protocol !== 'rtsp:') {
        return 'Protocol must be rtsp://';
      }
      if (!urlObj.hostname) {
        return 'Invalid hostname in URL';
      }
    } catch {
      return 'Invalid URL format';
    }

    return null;
  }

  function validateName(name: string): string | null {
    if (!name || name.trim().length === 0) {
      return 'Name is required';
    }

    if (name.trim().length > 50) {
      return 'Name must be less than 50 characters';
    }

    return null;
  }

  function generateId(): string {
    return `rtsp-${crypto.randomUUID()}`;
  }

  function addUrl() {
    const trimmedUrl = newUrl.trim();
    const trimmedName = newName.trim();

    if (!trimmedUrl || !trimmedName) return;

    const urlValidation = validateRTSPUrl(trimmedUrl);
    const nameValidation = validateName(trimmedName);

    if (urlValidation || nameValidation) {
      return; // Don't add invalid URLs
    }

    // Check for duplicate URLs
    if (urls.some(item => item.url === trimmedUrl)) {
      return; // Don't add duplicates
    }

    if (urls.length >= maxItems) {
      return; // Don't exceed max items
    }

    const newRTSPUrl: RTSPUrl = {
      id: generateId(),
      url: trimmedUrl,
      name: trimmedName,
      active: true,
    };

    const updated = [...urls, newRTSPUrl];
    onUpdate(updated);
    newUrl = '';
    newName = '';
  }

  function removeUrl(id: string) {
    const updated = urls.filter(item => item.id !== id);
    onUpdate(updated);

    // Clear errors for removed item
    errors.delete(id);
  }

  function updateUrl(id: string, field: keyof RTSPUrl, value: RTSPUrl[keyof RTSPUrl]) {
    const updated = urls.map(url => {
      if (url.id === id) {
        const updatedUrl = { ...url };
        Object.assign(updatedUrl, { [field]: value });
        return updatedUrl;
      }
      return url;
    });
    onUpdate(updated);
  }

  // Reactive validation for URLs - runs when urls change due to two-way binding
  $effect(() => {
    urls.forEach(url => {
      // Validate URL
      const urlValidation = validateRTSPUrl(url.url);
      const currentUrlError = errors.get(url.id);

      if (urlValidation) {
        if (currentUrlError !== urlValidation) {
          errors.set(url.id, urlValidation);
        }
      } else if (currentUrlError) {
        errors.delete(url.id);
      }

      // Validate name
      const nameValidation = validateName(url.name);
      const nameKey = `${url.id}-name`;
      const currentNameError = errors.get(nameKey);

      if (nameValidation) {
        if (currentNameError !== nameValidation) {
          errors.set(nameKey, nameValidation);
        }
      } else if (currentNameError) {
        errors.delete(nameKey);
      }
    });
  });

  function handleKeyDown(event: KeyboardEvent) {
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault();
      addUrl();
    }
  }

  // Check if new URL input is valid
  let newUrlError = $derived.by(() => {
    if (!newUrl.trim()) return null;
    return validateRTSPUrl(newUrl.trim());
  });

  let newNameError = $derived.by(() => {
    if (!newName.trim()) return null;
    return validateName(newName.trim());
  });

  let canAdd = $derived(
    newUrl.trim().length > 0 &&
      newName.trim().length > 0 &&
      !newUrlError &&
      !newNameError &&
      !urls.some(item => item.url === newUrl.trim()) &&
      urls.length < maxItems
  );
</script>

<div class={cn('form-control', className)} {...rest}>
  <label for={fieldId} class="label">
    <span class="label-text font-medium">
      {label}
    </span>
  </label>

  <!-- Add new RTSP URL form -->
  <div class="border border-base-300 rounded-lg p-4 mb-4">
    <h4 class="font-medium mb-3">Add New RTSP Stream</h4>

    <div class="space-y-3">
      <!-- Stream Name -->
      <FormField
        type="text"
        name="rtsp-name"
        label="Stream Name"
        bind:value={newName}
        placeholder="Camera 1"
        helpText="Friendly name for this RTSP stream"
        required={true}
        {disabled}
        inputClassName={newNameError ? 'input-error' : ''}
      />
      {#if newNameError}
        <div class="text-error text-sm">{newNameError}</div>
      {/if}

      <!-- RTSP URL -->
      <div class="flex gap-2">
        <FormField
          type="text"
          name="rtsp-url"
          label="RTSP URL"
          bind:value={newUrl}
          placeholder="rtsp://username:password@192.168.1.100:554/stream"
          helpText="Complete RTSP stream URL including credentials if required"
          required={true}
          {disabled}
          className="flex-1"
          inputClassName={newUrlError ? 'input-error' : ''}
          onkeydown={handleKeyDown}
        />
        <div class="flex items-end">
          <button
            type="button"
            class="btn btn-primary"
            onclick={addUrl}
            disabled={disabled || !canAdd}
            aria-label="Add RTSP URL"
          >
            <Plus class="size-4" />
            Add
          </button>
        </div>
      </div>
      {#if newUrlError}
        <div class="text-error text-sm">{newUrlError}</div>
      {/if}
    </div>
  </div>

  <!-- Help text -->
  {#if helpText}
    <div class="label">
      <span class="label-text-alt">{helpText}</span>
    </div>
  {/if}

  <!-- RTSP URLs list -->
  {#if urls.length > 0}
    <div class="space-y-3">
      <div class="flex items-center justify-between">
        <div class="text-sm font-medium text-base-content/70">
          Configured RTSP Streams ({urls.length}/{maxItems}):
        </div>
        {#if urls.length > 1}
          <!-- TODO: Add drag and drop functionality for reordering -->
        {/if}
      </div>

      {#each urls as rtspUrl, index (rtspUrl.id)}
        <div class="card bg-base-200 p-4">
          <div class="space-y-3">
            <!-- Header with name and controls -->
            <div class="flex items-center justify-between">
              <div class="flex items-center gap-3">
                <div class="font-medium">{rtspUrl.name}</div>
                <div class="badge badge-sm {rtspUrl.active ? 'badge-success' : 'badge-neutral'}">
                  {rtspUrl.active ? 'Active' : 'Inactive'}
                </div>
              </div>
              <div class="flex items-center gap-2">
                <button
                  type="button"
                  class="btn btn-ghost btn-sm btn-square text-error"
                  onclick={() => removeUrl(rtspUrl.id)}
                  {disabled}
                  aria-label="Remove RTSP stream"
                >
                  <X class="size-4" />
                </button>
              </div>
            </div>

            <!-- URL and name editing -->
            <div class="grid grid-cols-1 lg:grid-cols-2 gap-3">
              <!-- Stream Name -->
              <div>
                <FormField
                  type="text"
                  name="stream-name-{rtspUrl.id}"
                  label="Stream Name"
                  bind:value={rtspUrl.name}
                  onChange={value => updateUrl(rtspUrl.id, 'name', String(value))}
                  onInput={value => updateUrl(rtspUrl.id, 'name', String(value))}
                  {disabled}
                  inputClassName={errors.get(`${rtspUrl.id}-name`) ? 'input-error' : ''}
                />
                {#if errors.get(`${rtspUrl.id}-name`)}
                  <div class="text-error text-sm mt-1">{errors.get(`${rtspUrl.id}-name`)}</div>
                {/if}
              </div>

              <!-- RTSP URL -->
              <div>
                <FormField
                  type="text"
                  name="stream-url-{rtspUrl.id}"
                  label="RTSP URL"
                  bind:value={rtspUrl.url}
                  onChange={value => updateUrl(rtspUrl.id, 'url', String(value))}
                  onInput={value => updateUrl(rtspUrl.id, 'url', String(value))}
                  {disabled}
                  inputClassName={errors.get(rtspUrl.id) ? 'input-error' : ''}
                />
                {#if errors.get(rtspUrl.id)}
                  <div class="text-error text-sm mt-1">{errors.get(rtspUrl.id)}</div>
                {/if}
              </div>
            </div>

            <!-- Stream controls -->
            <div class="flex items-center justify-between">
              <ToggleField
                label="Enable Stream"
                description="Include this stream in audio processing"
                value={rtspUrl.active}
                onUpdate={value => updateUrl(rtspUrl.id, 'active', value)}
                {disabled}
              />

              <div class="text-xs text-base-content/60">
                Stream {index + 1} of {urls.length}
              </div>
            </div>
          </div>
        </div>
      {/each}
    </div>
  {:else}
    <div class="text-center py-8 text-base-content/60 bg-base-200 rounded-lg">
      <div class="mb-2 flex justify-center">
        <Video class="size-5" />
      </div>
      <div class="text-sm font-medium">No RTSP streams configured</div>
      <div class="text-xs">Add RTSP camera streams for audio capture</div>
    </div>
  {/if}

  <!-- Max items warning -->
  {#if urls.length >= maxItems}
    <div class="alert alert-warning mt-3">
      <TriangleAlert class="size-4" />
      <span>Maximum number of RTSP streams ({maxItems}) reached.</span>
    </div>
  {/if}

  <!-- Main error display -->
  {#if error}
    <div class="label">
      <span class="label-text-alt text-error">{error}</span>
    </div>
  {/if}
</div>
