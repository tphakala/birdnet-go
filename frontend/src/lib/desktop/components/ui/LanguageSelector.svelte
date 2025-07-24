<script lang="ts">
  import { getLocale, setLocale } from '$lib/i18n/store.svelte.js';
  import { LOCALES, type Locale } from '$lib/i18n/config.js';
  import { t } from '$lib/i18n';

  // Props
  interface Props {
    className?: string;
  }

  let { className = '' }: Props = $props();

  // Get current locale
  let currentLocale = $derived(getLocale());

  /**
   * Handle language selection change
   */
  function handleLanguageChange(event: Event) {
    const target = event.target as HTMLSelectElement;
    const newLocale = target.value as Locale;

    if (newLocale === currentLocale) return;

    // Update the locale in the store
    setLocale(newLocale);

    // Store preference in localStorage
    if (typeof localStorage !== 'undefined') {
      localStorage.setItem('birdnet-locale', newLocale);
    }
  }
</script>

<select
  class="select select-bordered select-sm {className}"
  value={currentLocale}
  onchange={handleLanguageChange}
  aria-label={t('common.aria.selectLanguage')}
>
  {#each Object.entries(LOCALES) as [code, { name, flag }]}
    <option value={code}>
      {flag}
      {name}
    </option>
  {/each}
</select>
