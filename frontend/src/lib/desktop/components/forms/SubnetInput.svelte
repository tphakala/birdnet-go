<script lang="ts">
  import { cn } from '$lib/utils/cn.js';
  import type { HTMLAttributes } from 'svelte/elements';

  interface Props extends HTMLAttributes<HTMLDivElement> {
    label: string;
    subnets: string[];
    onUpdate: (_subnets: string[]) => void;
    helpText?: string;
    disabled?: boolean;
    error?: string;
    className?: string;
    placeholder?: string;
    maxItems?: number;
    emptyStateMessage?: string;
  }

  let {
    label,
    subnets,
    onUpdate,
    helpText = '',
    disabled = false,
    error,
    className = '',
    placeholder = '192.168.1.0/24',
    maxItems = 10,
    emptyStateMessage = 'Add subnet ranges to configure network access',
    ...rest
  }: Props = $props();

  let newSubnet = $state('');
  let fieldId = `subnet-${Math.random().toString(36).substring(2, 11)}`;
  let errors = $state<Record<number, string>>({});

  // Validation function for CIDR notation
  function validateCIDR(cidr: string): string | null {
    if (!cidr || cidr.trim().length === 0) {
      return 'Subnet cannot be empty';
    }

    const trimmed = cidr.trim();

    // Basic CIDR format check
    const cidrPattern = /^(\d{1,3}\.){3}\d{1,3}\/\d{1,2}$/;
    if (!cidrPattern.test(trimmed)) {
      return 'Invalid CIDR format. Use format like 192.168.1.0/24';
    }

    const [ip, prefix] = trimmed.split('/');
    const prefixNum = parseInt(prefix, 10);

    // Validate prefix length
    if (prefixNum < 0 || prefixNum > 32) {
      return 'Prefix length must be between 0 and 32';
    }

    // Validate IP address octets
    const octets = ip.split('.');
    for (const octet of octets) {
      const num = parseInt(octet, 10);
      if (isNaN(num) || num < 0 || num > 255) {
        return 'Invalid IP address. Each octet must be 0-255';
      }
    }

    return null;
  }

  function addSubnet() {
    const trimmed = newSubnet.trim();
    if (!trimmed) return;

    const validation = validateCIDR(trimmed);
    if (validation) {
      return; // Don't add invalid subnets
    }

    if (subnets.includes(trimmed)) {
      return; // Don't add duplicates
    }

    if (subnets.length >= maxItems) {
      return; // Don't exceed max items
    }

    const updated = [...subnets, trimmed];
    onUpdate(updated);
    newSubnet = '';
  }

  function removeSubnet(index: number) {
    const updated = subnets.filter((_, i) => i !== index);
    onUpdate(updated);

    // Clear error for removed item
    const newErrors = { ...errors };
    delete newErrors[index];
    errors = newErrors;
  }

  function updateSubnet(index: number, value: string) {
    const updated = [...subnets];
    updated[index] = value;

    // Validate the updated subnet
    const validation = validateCIDR(value);
    if (validation) {
      errors[index] = validation;
    } else {
      const newErrors = { ...errors };
      delete newErrors[index];
      errors = newErrors;
    }

    onUpdate(updated);
  }

  function handleKeyDown(event: KeyboardEvent) {
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault();
      addSubnet();
    }
  }

  function handleNewSubnetInput(event: Event) {
    const target = event.target as HTMLInputElement;
    newSubnet = target.value;
  }

  // Check if new subnet input is valid
  let newSubnetError = $derived.by(() => {
    if (!newSubnet.trim()) return null;
    return validateCIDR(newSubnet.trim());
  });

  let canAdd = $derived(
    newSubnet.trim().length > 0 &&
      !newSubnetError &&
      !subnets.includes(newSubnet.trim()) &&
      subnets.length < maxItems
  );
</script>

<div class={cn('form-control', className)} {...rest}>
  <label for={fieldId} class="label">
    <span class="label-text font-medium">
      {label}
    </span>
  </label>

  <!-- Add new subnet input -->
  <div class="flex gap-2 mb-3">
    <input
      id={fieldId}
      type="text"
      bind:value={newSubnet}
      {placeholder}
      {disabled}
      class={cn('input input-bordered flex-1', newSubnetError ? 'input-error' : '')}
      onkeydown={handleKeyDown}
      oninput={handleNewSubnetInput}
      aria-describedby={helpText ? `${fieldId}-help` : undefined}
    />
    <button
      type="button"
      class="btn btn-primary"
      onclick={addSubnet}
      disabled={disabled || !canAdd}
      aria-label="Add subnet"
    >
      <svg
        xmlns="http://www.w3.org/2000/svg"
        class="h-5 w-5"
        fill="none"
        viewBox="0 0 24 24"
        stroke="currentColor"
      >
        <path
          stroke-linecap="round"
          stroke-linejoin="round"
          stroke-width="2"
          d="M12 6v6m0 0v6m0-6h6m-6 0H6"
        />
      </svg>
      Add
    </button>
  </div>

  <!-- New subnet input error -->
  {#if newSubnetError}
    <div class="text-error text-sm mb-2">{newSubnetError}</div>
  {/if}

  <!-- Help text -->
  {#if helpText}
    <div id="{fieldId}-help" class="label">
      <span class="label-text-alt">{helpText}</span>
    </div>
  {/if}

  <!-- Subnet list -->
  {#if subnets.length > 0}
    <div class="space-y-2 mt-2">
      <div class="text-sm font-medium text-base-content/70">
        Allowed Subnets ({subnets.length}/{maxItems}):
      </div>

      {#each subnets as subnet, index}
        <div class="flex items-center gap-2 p-2 bg-base-200 rounded-lg">
          <input
            type="text"
            value={subnet}
            oninput={e => updateSubnet(index, (e.target as HTMLInputElement)?.value || '')}
            {disabled}
            class={cn('input input-sm input-bordered flex-1', errors[index] ? 'input-error' : '')}
          />
          <button
            type="button"
            class="btn btn-ghost btn-sm btn-square text-error"
            onclick={() => removeSubnet(index)}
            {disabled}
            aria-label="Remove subnet"
          >
            <svg
              xmlns="http://www.w3.org/2000/svg"
              class="h-4 w-4"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
            >
              <path
                stroke-linecap="round"
                stroke-linejoin="round"
                stroke-width="2"
                d="M6 18L18 6M6 6l12 12"
              />
            </svg>
          </button>
        </div>

        {#if errors[index]}
          <div class="text-error text-sm ml-2">{errors[index]}</div>
        {/if}
      {/each}
    </div>
  {:else}
    <div class="text-center py-4 text-base-content/50 bg-base-200 rounded-lg mt-2">
      <div class="text-sm">No subnets configured</div>
      <div class="text-xs">{emptyStateMessage}</div>
    </div>
  {/if}

  <!-- Max items warning -->
  {#if subnets.length >= maxItems}
    <div class="alert alert-warning mt-2">
      <svg
        xmlns="http://www.w3.org/2000/svg"
        class="stroke-current shrink-0 h-6 w-6"
        fill="none"
        viewBox="0 0 24 24"
      >
        <path
          stroke-linecap="round"
          stroke-linejoin="round"
          stroke-width="2"
          d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-2.5L13.732 4c-.77-.833-1.964-.833-2.732 0L3.082 16.5c-.77.833.192 2.5 1.732 2.5z"
        />
      </svg>
      <span>Maximum number of subnets ({maxItems}) reached.</span>
    </div>
  {/if}

  <!-- Main error display -->
  {#if error}
    <div class="label">
      <span class="label-text-alt text-error">{error}</span>
    </div>
  {/if}
</div>
