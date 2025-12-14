<script lang="ts">
  import { getLocale, setLocale } from '$lib/i18n/store.svelte.js';
  import { LOCALES, type Locale } from '$lib/i18n/config.js';
  import SelectDropdown from '$lib/desktop/components/forms/SelectDropdown.svelte';
  import type { SelectOption } from '$lib/desktop/components/forms/SelectDropdown.types';
  import FlagIcon, { type FlagLocale } from '$lib/desktop/components/ui/FlagIcon.svelte';

  // Props
  interface Props {
    className?: string;
  }

  let { className = '' }: Props = $props();

  // Extended option type for locale with typed locale code
  interface LocaleOption extends SelectOption {
    localeCode: FlagLocale;
  }

  // Static locale options
  const localeOptions: LocaleOption[] = Object.entries(LOCALES).map(([code, info]) => ({
    value: code,
    label: info.name,
    localeCode: code as FlagLocale,
  }));

  // Get current locale
  let currentLocale = $derived(getLocale());

  /**
   * Handle language selection change
   * Note: groupBy={false} means single selection, so value is always string at runtime
   */
  function handleLanguageChange(value: string | string[]) {
    // With groupBy={false}, value is always a single string, but the type signature
    // must match SelectDropdown's onChange for TypeScript compatibility
    const newLocale = (Array.isArray(value) ? value[0] : value) as Locale;

    if (newLocale === currentLocale) return;

    // Update the locale in the store
    setLocale(newLocale);

    // Store preference in localStorage
    if (typeof localStorage !== 'undefined') {
      localStorage.setItem('birdnet-locale', newLocale);
    }
  }
</script>

<SelectDropdown
  options={localeOptions}
  value={currentLocale}
  variant="select"
  size="sm"
  groupBy={false}
  {className}
  onChange={handleLanguageChange}
>
  {#snippet renderOption(option)}
    {@const localeOption = option as LocaleOption}
    <div class="flex items-center gap-2">
      <FlagIcon locale={localeOption.localeCode} className="size-4" />
      <span>{localeOption.label}</span>
    </div>
  {/snippet}
  {#snippet renderSelected(options)}
    {#if options.length > 0}
      {@const localeOption = options[0] as LocaleOption}
      <span class="flex items-center gap-2">
        <FlagIcon locale={localeOption.localeCode} className="size-4" />
        <span>{localeOption.label}</span>
      </span>
    {/if}
  {/snippet}
</SelectDropdown>
