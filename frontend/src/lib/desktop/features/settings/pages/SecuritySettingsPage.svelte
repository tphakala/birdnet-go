<!--
  Security Settings Page Component

  Purpose: Configure authentication and access control for BirdNET-Go including
  HTTPS/TLS settings, basic authentication, OAuth2 social login providers, and
  subnet-based authentication bypass.

  Features:
  - Server configuration with automatic TLS via Let's Encrypt
  - Basic authentication with password protection
  - OAuth2 integration (Google, GitHub, Microsoft) with dynamic provider management
  - Subnet-based authentication bypass for local networks
  - Dynamic redirect URI generation based on host settings
  - Real-time validation and change detection

  Props: None - This is a page component that uses global settings stores

  Performance Optimizations:
  - Removed page-level loading spinner to prevent flickering
  - Reactive settings with $derived instead of $state + $effect
  - Cached CSRF token to avoid repeated DOM queries
  - Reactive change detection with $derived
  - Dynamic redirect URI generation based on current host

  @component
-->
<script lang="ts">
  import Checkbox from '$lib/desktop/components/forms/Checkbox.svelte';
  import TextInput from '$lib/desktop/components/forms/TextInput.svelte';
  import PasswordField from '$lib/desktop/components/forms/PasswordField.svelte';
  import SelectDropdown from '$lib/desktop/components/forms/SelectDropdown.svelte';
  import type { SelectOption } from '$lib/desktop/components/forms/SelectDropdown.types';
  import SettingsSection from '$lib/desktop/features/settings/components/SettingsSection.svelte';
  import SettingsNote from '$lib/desktop/features/settings/components/SettingsNote.svelte';
  import SettingsTabs from '$lib/desktop/features/settings/components/SettingsTabs.svelte';
  import type { TabDefinition } from '$lib/desktop/features/settings/components/SettingsTabs.svelte';
  import {
    settingsStore,
    settingsActions,
    securitySettings,
    type OAuthProviderConfig,
  } from '$lib/stores/settings';
  import { hasSettingsChanged } from '$lib/utils/settingsChanges';
  import { TriangleAlert, ExternalLink, Server, KeyRound, Users, Network, Plus, Pencil, Trash2, Terminal } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { GoogleIcon, AUTH_PROVIDERS } from '$lib/auth';
  import type { Component } from 'svelte';

  // Provider type for OAuth providers
  type OAuthProviderType = 'google' | 'github' | 'microsoft' | 'line' | 'kakao';

  // OAuth provider option for dropdown
  interface OAuthProviderOption extends SelectOption {
    providerId: OAuthProviderType;
  }

  // All available OAuth providers
  const allOAuthProviders: OAuthProviderOption[] = [
    { value: 'google', label: 'Google', providerId: 'google' },
    { value: 'github', label: 'GitHub', providerId: 'github' },
    { value: 'microsoft', label: 'Microsoft', providerId: 'microsoft' },
    { value: 'line', label: 'LINE', providerId: 'line' },
    { value: 'kakao', label: 'Kakao', providerId: 'kakao' },
  ];

  // PERFORMANCE OPTIMIZATION: Reactive settings with proper defaults
  let settings = $derived(
    $securitySettings || {
      baseUrl: '',
      host: '',
      autoTls: false,
      basicAuth: {
        enabled: false,
        username: '',
        password: '',
      },
      oauthProviders: [],
      allowSubnetBypass: {
        enabled: false,
        subnet: '',
      },
    }
  );

  // OAuth providers from settings
  let oauthProviders = $derived(settings?.oauthProviders ?? []);

  let store = $derived($settingsStore);

  // PERFORMANCE OPTIMIZATION: Reactive change detection with $derived
  let serverConfigHasChanges = $derived(
    hasSettingsChanged(
      {
        baseUrl: store.originalData.security?.baseUrl,
        host: store.originalData.security?.host,
        autoTls: store.originalData.security?.autoTls,
      },
      {
        baseUrl: store.formData.security?.baseUrl,
        host: store.formData.security?.host,
        autoTls: store.formData.security?.autoTls,
      }
    )
  );

  let basicAuthHasChanges = $derived(
    hasSettingsChanged(
      store.originalData.security?.basicAuth,
      store.formData.security?.basicAuth
    )
  );

  let oauthHasChanges = $derived(
    hasSettingsChanged(
      store.originalData.security?.oauthProviders,
      store.formData.security?.oauthProviders
    )
  );

  // OAuth provider form state
  let showProviderForm = $state(false);
  let editingProviderIndex = $state<number | null>(null);
  let selectedProvider = $state<OAuthProviderType>('google');
  let providerFormData = $state<{
    clientId: string;
    clientSecret: string;
    userId: string;
    enabled: boolean;
  }>({
    clientId: '',
    clientSecret: '',
    userId: '',
    enabled: true,
  });

  // Available providers (filter out already configured ones)
  let availableProviders = $derived(
    allOAuthProviders.filter(
      p => !oauthProviders.some(configured => configured.provider === p.providerId)
    )
  );

  // Disable "Add" button when all providers are configured
  let canAddProvider = $derived(availableProviders.length > 0);

  let subnetBypassHasChanges = $derived(
    hasSettingsChanged(
      store.originalData.security?.allowSubnetBypass,
      store.formData.security?.allowSubnetBypass
    )
  );

  // PERFORMANCE OPTIMIZATION: Generate redirect URIs dynamically with $derived
  // Use window.location.origin for display (what the user sees in browser)
  let currentHost = $derived(
    typeof window !== 'undefined'
      ? window.location.origin
      : settings?.host
        ? `https://${settings.host}`
        : 'https://your-domain.com'
  );

  // Canonical base URL for saving to config (uses configured host/baseUrl)
  // This is what gets persisted to config.yaml for OAuth callbacks
  // Returns empty string if neither is configured - backend will auto-generate from its config
  let configuredBaseUrl = $derived.by(() => {
    if (settings?.baseUrl) {
      return settings.baseUrl.replace(/\/$/, ''); // Remove trailing slash
    }
    if (settings?.host) {
      const host = settings.host.replace(/\/$/, '');
      // If host already has scheme, use as-is
      if (host.startsWith('http://') || host.startsWith('https://')) {
        return host;
      }
      // Otherwise assume HTTPS
      return `https://${host}`;
    }
    // Return empty - backend will auto-generate redirect URI from its own baseURL/host config
    // This prevents saving incorrect URLs when admin UI is accessed from local IP
    return '';
  });

  // Whether we have explicit base URL configuration for redirect URIs
  let hasExplicitBaseUrl = $derived(configuredBaseUrl !== '');

  // Helper function to get redirect URI for a provider (for display)
  function getRedirectURI(providerType: OAuthProviderType): string {
    // eslint-disable-next-line security/detect-object-injection -- providerType is typed as OAuthProviderType enum, not user input
    const provider = AUTH_PROVIDERS[providerType];
    return `${currentHost}${provider?.settings.callbackPath || `/auth/${providerType}/callback`}`;
  }

  // Helper function to get config redirect URI for a provider (for saving)
  function getConfigRedirectURI(providerType: OAuthProviderType): string {
    // eslint-disable-next-line security/detect-object-injection -- providerType is typed as OAuthProviderType enum, not user input
    const provider = AUTH_PROVIDERS[providerType];
    return `${configuredBaseUrl}${provider?.settings.callbackPath || `/auth/${providerType}/callback`}`;
  }

  // Helper to get provider icon component from AUTH_PROVIDERS registry
  function getProviderIcon(providerType: OAuthProviderType): Component {
    // eslint-disable-next-line security/detect-object-injection -- providerType is typed as OAuthProviderType enum, not user input
    return AUTH_PROVIDERS[providerType]?.icon ?? GoogleIcon;
  }

  // Helper to get provider display name from AUTH_PROVIDERS registry
  function getProviderDisplayName(providerType: OAuthProviderType): string {
    // eslint-disable-next-line security/detect-object-injection -- providerType is typed as OAuthProviderType enum, not user input
    return AUTH_PROVIDERS[providerType]?.name ?? providerType;
  }

  // Helper to get credentials URL for a provider
  function getCredentialsUrl(providerType: OAuthProviderType): string {
    // eslint-disable-next-line security/detect-object-injection -- providerType is typed as OAuthProviderType enum, not user input
    const provider = AUTH_PROVIDERS[providerType];
    return provider?.settings.credentialsUrl || '';
  }

  // Server Configuration update handlers
  function updateBaseUrl(baseUrl: string) {
    settingsActions.updateSection('security', {
      ...settings,
      baseUrl: baseUrl,
    });
  }

  function updateAutoTLSEnabled(enabled: boolean) {
    settingsActions.updateSection('security', {
      ...settings,
      autoTls: enabled,
    });
  }

  function updateAutoTLSHost(host: string) {
    settingsActions.updateSection('security', {
      ...settings,
      host: host,
    });
  }

  // Basic Auth update handlers
  function updateBasicAuthEnabled(enabled: boolean) {
    settingsActions.updateSection('security', {
      ...settings,
      basicAuth: { ...settings.basicAuth, enabled },
    });
  }

  function updateBasicAuthPassword(password: string) {
    settingsActions.updateSection('security', {
      ...settings,
      basicAuth: { ...settings.basicAuth, password },
    });
  }

  // OAuth Provider management functions
  function openAddProviderForm() {
    if (!canAddProvider) return;
    editingProviderIndex = null;
    selectedProvider = availableProviders[0]?.providerId ?? 'google';
    providerFormData = {
      clientId: '',
      clientSecret: '',
      userId: '',
      enabled: true,
    };
    showProviderForm = true;
  }

  function openEditProviderForm(index: number) {
    // eslint-disable-next-line security/detect-object-injection -- index is derived from array iteration, validated below
    const provider = oauthProviders[index];
    if (!provider) return;

    editingProviderIndex = index;
    selectedProvider = provider.provider as OAuthProviderType;
    providerFormData = {
      clientId: provider.clientId,
      clientSecret: provider.clientSecret,
      userId: provider.userId ?? '',
      enabled: provider.enabled,
    };
    showProviderForm = true;
  }

  function closeProviderForm() {
    showProviderForm = false;
    editingProviderIndex = null;
  }

  function saveProvider() {
    // Build the provider config
    const newProvider: OAuthProviderConfig = {
      provider: selectedProvider,
      enabled: providerFormData.enabled,
      clientId: providerFormData.clientId,
      clientSecret: providerFormData.clientSecret,
      userId: providerFormData.userId || undefined,
      // Set redirectUri only if we have explicit base URL configuration
      redirectUri: hasExplicitBaseUrl ? getConfigRedirectURI(selectedProvider) : undefined,
    };

    // Update the providers array
    const updatedProviders = [...oauthProviders];
    if (editingProviderIndex !== null) {
      // Update existing provider
      // eslint-disable-next-line security/detect-object-injection -- editingProviderIndex is from our state, validated as not null
      updatedProviders[editingProviderIndex] = newProvider;
    } else {
      // Add new provider
      updatedProviders.push(newProvider);
    }

    // Update settings
    settingsActions.updateSection('security', {
      ...settings,
      oauthProviders: updatedProviders,
    });

    closeProviderForm();
  }

  function deleteProvider(index: number) {
    // eslint-disable-next-line security/detect-object-injection -- index is derived from array iteration, validated below
    const provider = oauthProviders[index];
    if (!provider) return;

    const providerName = getProviderDisplayName(provider.provider as OAuthProviderType);
    const confirmDelete = window.confirm(
      t('settings.security.oauth.providers.deleteConfirm', { provider: providerName })
    );

    if (confirmDelete) {
      const updatedProviders = oauthProviders.filter((_, i) => i !== index);
      settingsActions.updateSection('security', {
        ...settings,
        oauthProviders: updatedProviders,
      });
    }
  }

  function toggleProviderEnabled(index: number) {
    // eslint-disable-next-line security/detect-object-injection -- index is derived from array iteration, validated below
    const provider = oauthProviders[index];
    if (!provider) return;

    const updatedProviders = [...oauthProviders];
    // eslint-disable-next-line security/detect-object-injection -- index is derived from array iteration, validated above
    updatedProviders[index] = {
      ...provider,
      enabled: !provider.enabled,
    };

    settingsActions.updateSection('security', {
      ...settings,
      oauthProviders: updatedProviders,
    });
  }

  // Subnet Bypass update handlers
  function updateSubnetBypassEnabled(enabled: boolean) {
    settingsActions.updateSection('security', {
      ...settings,
      allowSubnetBypass: { ...settings.allowSubnetBypass, enabled },
    });
  }

  function updateSubnetBypassSubnet(subnet: string) {
    settingsActions.updateSection('security', {
      ...settings,
      allowSubnetBypass: { ...settings.allowSubnetBypass, subnet },
    });
  }

  // Terminal toggle — reads from webServer section of the settings store
  let webServerData = $derived($settingsStore.formData.webServer);
  let enableTerminal = $derived(webServerData?.enableTerminal ?? false);

  // Change detection for the save bar
  let terminalHasChanges = $derived(
    hasSettingsChanged(
      { enableTerminal: $settingsStore.originalData.webServer?.enableTerminal ?? false },
      { enableTerminal: enableTerminal }
    )
  );

  function handleTerminalToggle(newValue: boolean) {
    if (newValue) {
      const confirmed = window.confirm(t('settings.security.terminal.confirmEnable'));
      if (!confirmed) return;
    }
    settingsActions.updateSection('webServer', { enableTerminal: newValue });
  }

  // Tab state
  let activeTab = $state('server');

  // Tab definitions
  let tabs = $derived<TabDefinition[]>([
    {
      id: 'server',
      label: t('settings.security.serverConfiguration.title'),
      icon: Server,
      content: serverTabContent,
      hasChanges: serverConfigHasChanges,
    },
    {
      id: 'basic-auth',
      label: t('settings.security.basicAuthentication.title'),
      icon: KeyRound,
      content: basicAuthTabContent,
      hasChanges: basicAuthHasChanges,
    },
    {
      id: 'oauth',
      label: t('settings.security.oauth.title'),
      icon: Users,
      content: oauthTabContent,
      hasChanges: oauthHasChanges,
    },
    {
      id: 'subnet',
      label: t('settings.security.bypassAuthentication.title'),
      icon: Network,
      content: subnetTabContent,
      hasChanges: subnetBypassHasChanges,
    },
    {
      id: 'terminal',
      label: t('settings.security.terminal.title'),
      icon: Terminal,
      content: terminalTabContent,
      hasChanges: terminalHasChanges,
    },
  ]);
</script>

{#snippet serverTabContent()}
  <div class="space-y-6">
    <!-- Server Configuration Card -->
    <SettingsSection
      title={t('settings.security.serverConfiguration.title')}
      description={t('settings.security.serverConfiguration.description')}
      originalData={{
        baseUrl: store.originalData.security?.baseUrl,
        host: store.originalData.security?.host,
        autoTls: store.originalData.security?.autoTls,
      }}
      currentData={{
        baseUrl: store.formData.security?.baseUrl,
        host: store.formData.security?.host,
        autoTls: store.formData.security?.autoTls,
      }}
    >
    <div class="space-y-4">
      <!-- Base URL (for reverse proxy setups) -->
      <TextInput
        id="base-url"
        type="url"
        value={settings.baseUrl}
        label={t('settings.security.baseUrlLabel')}
        placeholder={t('settings.security.placeholders.baseUrl')}
        helpText={t('settings.security.baseUrlHelp')}
        pattern="^https?://[^/:]+.*$"
        validationMessage={t('settings.security.baseUrlValidation')}
        disabled={store.isLoading || store.isSaving}
        onchange={updateBaseUrl}
      />

      <!-- Host Address -->
      <TextInput
        id="host-address"
        value={settings.host}
        label={t('settings.security.hostLabel')}
        placeholder={t('settings.security.placeholders.host')}
        disabled={store.isLoading || store.isSaving}
        onchange={updateAutoTLSHost}
      />

      <div class="border-t border-[var(--border-100)] pt-4 mt-4">
        <h4 class="text-lg font-medium mb-2">{t('settings.security.httpsSettingsTitle')}</h4>
        <p class="text-sm text-[color:var(--color-base-content)] opacity-70 mb-4">
          {t('settings.security.httpsSettingsDescription')}
        </p>

        <Checkbox
          checked={settings.autoTls}
          label={t('settings.security.serverConfiguration.autoTlsLabel')}
          disabled={store.isLoading || store.isSaving}
          onchange={updateAutoTLSEnabled}
        />

        <SettingsNote>
          <p><strong>{t('settings.security.serverConfiguration.autoTlsRequirements.title')}</strong></p>
          <ul class="list-disc list-inside mt-1">
            <li>{t('settings.security.serverConfiguration.autoTlsRequirements.domainRequired')}</li>
            <li>{t('settings.security.serverConfiguration.autoTlsRequirements.domainPointing')}</li>
            <li>{t('settings.security.serverConfiguration.autoTlsRequirements.portsAccessible')}</li>
          </ul>
        </SettingsNote>
        
      </div>
    </div>
    </SettingsSection>
  </div>
{/snippet}

{#snippet basicAuthTabContent()}
  <div class="space-y-6">
    <!-- Basic Authentication Card -->
    <SettingsSection
      title={t('settings.security.basicAuthentication.title')}
      description={t('settings.security.basicAuthentication.description')}
      originalData={store.originalData.security?.basicAuth}
      currentData={store.formData.security?.basicAuth}
    >
      <form class="space-y-4" onsubmit={(e) => e.preventDefault()} autocomplete="off">
        <Checkbox
          checked={settings.basicAuth.enabled}
          label={t('settings.security.basicAuthentication.enableLabel')}
          disabled={store.isLoading || store.isSaving}
          onchange={updateBasicAuthEnabled}
        />

        <!-- Fieldset for accessible disabled state - all inputs greyed out when feature disabled -->
        <fieldset
          disabled={!settings.basicAuth?.enabled || store.isLoading || store.isSaving}
          class="contents"
          aria-describedby="basic-auth-status"
        >
          <span id="basic-auth-status" class="sr-only">
            {settings.basicAuth?.enabled
              ? t('settings.security.basicAuthentication.enableLabel')
              : t('settings.security.basicAuthentication.disabled')}
          </span>
          <div
            class="transition-opacity duration-200"
            class:opacity-50={!settings.basicAuth?.enabled}
          >
            <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
              <PasswordField
                label={t('settings.security.basicAuthentication.passwordLabel')}
                value={settings.basicAuth.password}
                onUpdate={updateBasicAuthPassword}
                placeholder=""
                helpText={t('settings.security.basicAuthentication.passwordHelpText')}
                disabled={!settings.basicAuth?.enabled || store.isLoading || store.isSaving}
                allowReveal={true}
              />
            </div>
          </div>
        </fieldset>
      </form>
    </SettingsSection>
  </div>
{/snippet}

{#snippet oauthTabContent()}
  <div class="space-y-6">
    <!-- OAuth2 Social Authentication Card -->
    <SettingsSection
      title={t('settings.security.oauth.title')}
      description={t('settings.security.oauth.description')}
      originalData={store.originalData.security?.oauthProviders}
      currentData={store.formData.security?.oauthProviders}
    >
      <div class="space-y-4">
        <!-- Provider Form (shown when adding or editing) -->
        {#if showProviderForm}
          <div class="rounded-lg overflow-hidden bg-[var(--color-base-200)] border border-[var(--color-primary)]">
            <div class="p-6">
              <h3 class="flex items-center gap-2 text-base font-semibold">
                {editingProviderIndex !== null
                  ? t('settings.security.oauth.form.editTitle')
                  : t('settings.security.oauth.form.addTitle')}
              </h3>

              <div class="space-y-4 mt-4">
                <!-- Provider Selector (only for add mode) -->
                {#if editingProviderIndex === null}
                  <SelectDropdown
                    options={availableProviders}
                    bind:value={selectedProvider}
                    label={t('settings.security.oauth.form.providerLabel')}
                    helpText={t('settings.security.oauth.form.providerHelpText')}
                    variant="select"
                    groupBy={false}
                  >
                    {#snippet renderOption(option)}
                      {@const providerOption = option as OAuthProviderOption}
                      {@const IconComponent = getProviderIcon(providerOption.providerId)}
                      <div class="flex items-center gap-2">
                        <IconComponent class="size-4" />
                        <span>{providerOption.label}</span>
                      </div>
                    {/snippet}
                    {#snippet renderSelected(options)}
                      {@const providerOption = options[0] as OAuthProviderOption}
                      {@const IconComponent = getProviderIcon(providerOption.providerId)}
                      <span class="flex items-center gap-2">
                        <IconComponent class="size-4" />
                        <span>{providerOption.label}</span>
                      </span>
                    {/snippet}
                  </SelectDropdown>
                {:else}
                  <!-- Show provider name when editing (not editable) -->
                  {@const IconComponent = getProviderIcon(selectedProvider)}
                  <div class="flex items-center gap-2 text-lg font-medium">
                    <IconComponent class="size-5" />
                    <span>{getProviderDisplayName(selectedProvider)}</span>
                  </div>
                {/if}

                <!-- Redirect URI Information -->
                <div class="bg-[var(--color-base-300)] p-3 rounded-lg">
                  <div class="text-sm">
                    <p class="font-medium mb-1">{t('settings.security.oauth.redirectUriTitle')}</p>
                    <code class="text-xs bg-[var(--color-base-200)] px-2 py-1 rounded-sm break-all">{getRedirectURI(selectedProvider)}</code>
                  </div>
                  <a
                    href={getCredentialsUrl(selectedProvider)}
                    target="_blank"
                    rel="noopener noreferrer"
                    class="text-sm text-[var(--color-primary)] hover:opacity-80 inline-flex items-center gap-1 mt-2"
                  >
                    {t('settings.security.oauth.getCredentialsLabel', { provider: getProviderDisplayName(selectedProvider) })}
                    <ExternalLink class="size-4" />
                  </a>
                </div>

                <!-- Credentials Fields -->
                <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
                  <PasswordField
                    label={t('settings.security.oauth.clientIdLabel')}
                    value={providerFormData.clientId}
                    onUpdate={(value) => (providerFormData.clientId = value)}
                    placeholder=""
                    helpText={t('settings.security.oauth.clientIdHelpText')}
                    disabled={store.isLoading || store.isSaving}
                    allowReveal={true}
                  />

                  <PasswordField
                    label={t('settings.security.oauth.clientSecretLabel')}
                    value={providerFormData.clientSecret}
                    onUpdate={(value) => (providerFormData.clientSecret = value)}
                    placeholder=""
                    helpText={t('settings.security.oauth.clientSecretHelpText')}
                    disabled={store.isLoading || store.isSaving}
                    allowReveal={true}
                  />
                </div>

                <TextInput
                  id="oauth-user-id"
                  value={providerFormData.userId}
                  label={t('settings.security.oauth.userIdLabel')}
                  placeholder={t('settings.security.placeholders.allowedUsers')}
                  helpText={t('settings.security.oauth.userIdHelpText')}
                  disabled={store.isLoading || store.isSaving}
                  onchange={(value) => (providerFormData.userId = value)}
                />

                <Checkbox
                  checked={providerFormData.enabled}
                  label={t('settings.security.oauth.enableProviderLabel')}
                  disabled={store.isLoading || store.isSaving}
                  onchange={(enabled) => (providerFormData.enabled = enabled)}
                />

                <!-- Form Actions -->
                <div class="flex gap-2 justify-end pt-2">
                  <button
                    onclick={closeProviderForm}
                    class="inline-flex items-center justify-center gap-2 px-3 py-1.5 text-sm font-medium rounded-md cursor-pointer transition-all bg-transparent text-[var(--color-base-content)] hover:bg-black/5 dark:hover:bg-white/5 disabled:opacity-50 disabled:cursor-not-allowed"
                    disabled={store.isLoading || store.isSaving}
                  >
                    {t('settings.security.oauth.form.cancelButton')}
                  </button>
                  <button
                    onclick={saveProvider}
                    class="inline-flex items-center justify-center gap-2 px-3 py-1.5 text-sm font-medium rounded-md cursor-pointer transition-all bg-[var(--color-primary)] text-[var(--color-primary-content)] border border-[var(--color-primary)] hover:bg-[var(--color-primary-hover)] disabled:opacity-50 disabled:cursor-not-allowed"
                    disabled={store.isLoading || store.isSaving || !providerFormData.clientId || !providerFormData.clientSecret}
                  >
                    {t('settings.security.oauth.form.saveButton')}
                  </button>
                </div>
              </div>
            </div>
          </div>
        {/if}

        <!-- Providers List Header -->
        <div class="flex items-center justify-between">
          <h3 class="font-semibold text-sm">
            {t('settings.security.oauth.providers.title')}
          </h3>
          {#if !showProviderForm && canAddProvider}
            <button onclick={openAddProviderForm} class="inline-flex items-center justify-center gap-1 px-3 py-1.5 text-sm font-medium rounded-md cursor-pointer transition-all bg-[var(--color-primary)] text-[var(--color-primary-content)] border border-[var(--color-primary)] hover:bg-[var(--color-primary-hover)] disabled:opacity-50 disabled:cursor-not-allowed">
              <Plus class="size-4" />
              {t('settings.security.oauth.providers.addButton')}
            </button>
          {/if}
        </div>

        <!-- Providers List -->
        {#if oauthProviders.length > 0}
          <div class="space-y-2">
            {#each oauthProviders as provider, index (provider.provider)}
              {@const providerType = provider.provider as OAuthProviderType}
              {@const IconComponent = getProviderIcon(providerType)}
              <div
                class="rounded-lg overflow-hidden bg-[var(--color-base-200)]"
                class:opacity-50={!provider.enabled}
              >
                <div class="py-3 px-4">
                  <div class="flex items-center justify-between gap-4">
                    <div class="flex items-center gap-3 min-w-0">
                      <input
                        type="checkbox"
                        class="appearance-none w-10 h-5 rounded-full cursor-pointer transition-all relative bg-[var(--color-base-300)] before:content-[''] before:absolute before:top-0.5 before:left-0.5 before:w-4 before:h-4 before:rounded-full before:bg-[var(--color-base-100)] before:shadow-sm before:transition-transform checked:bg-[var(--color-primary)] checked:before:translate-x-5 focus-visible:outline-2 focus-visible:outline-[var(--color-primary)] focus-visible:outline-offset-2 disabled:opacity-50 disabled:cursor-not-allowed"
                        checked={provider.enabled}
                        onchange={() => toggleProviderEnabled(index)}
                        aria-label={t('settings.security.oauth.providers.enableToggle')}
                        disabled={showProviderForm}
                      />
                      <div class="flex items-center gap-2 min-w-0">
                        <IconComponent class="size-5 shrink-0" />
                        <div class="min-w-0">
                          <div class="font-medium truncate">{getProviderDisplayName(providerType)}</div>
                          {#if provider.userId}
                            <div class="text-xs text-[var(--color-base-content)] opacity-60 truncate">
                              {provider.userId}
                            </div>
                          {/if}
                        </div>
                      </div>
                    </div>
                    <div class="flex items-center gap-1 shrink-0">
                      <button
                        onclick={() => openEditProviderForm(index)}
                        class="inline-flex items-center justify-center p-1 aspect-square rounded-md cursor-pointer transition-all bg-transparent hover:bg-black/5 dark:hover:bg-white/5 disabled:opacity-50 disabled:cursor-not-allowed"
                        title={t('settings.security.oauth.providers.editButton')}
                        aria-label={t('settings.security.oauth.providers.editButton')}
                        disabled={showProviderForm}
                      >
                        <Pencil class="size-3.5" />
                      </button>
                      <button
                        onclick={() => deleteProvider(index)}
                        class="inline-flex items-center justify-center p-1 aspect-square rounded-md cursor-pointer transition-all bg-transparent hover:bg-black/5 dark:hover:bg-white/5 text-[var(--color-error)] disabled:opacity-50 disabled:cursor-not-allowed"
                        title={t('settings.security.oauth.providers.deleteButton')}
                        aria-label={t('settings.security.oauth.providers.deleteButton')}
                        disabled={showProviderForm}
                      >
                        <Trash2 class="size-3.5" />
                      </button>
                    </div>
                  </div>
                </div>
              </div>
            {/each}
          </div>
        {:else if !showProviderForm}
          <div class="text-center py-8 text-[var(--color-base-content)] opacity-60 bg-[var(--color-base-200)] rounded-lg">
            <Users class="size-10 mx-auto mb-3 opacity-50" />
            <p class="text-sm font-medium">{t('settings.security.oauth.noProviders')}</p>
            <p class="text-xs mt-1">
              {t('settings.security.oauth.noProvidersDescription')}
            </p>
          </div>
        {/if}
      </div>
    </SettingsSection>
  </div>
{/snippet}

{#snippet subnetTabContent()}
  <div class="space-y-6">
    <!-- Bypass Authentication Card -->
    <SettingsSection
      title={t('settings.security.bypassAuthentication.title')}
      description={t('settings.security.bypassAuthentication.description')}
      originalData={store.originalData.security?.allowSubnetBypass}
      currentData={store.formData.security?.allowSubnetBypass}
    >
      <div class="space-y-4">
        <Checkbox
          checked={settings.allowSubnetBypass.enabled}
          label={t('settings.security.allowSubnetBypassLabel')}
          disabled={store.isLoading || store.isSaving}
          onchange={updateSubnetBypassEnabled}
        />

        <!-- Fieldset for accessible disabled state - all inputs greyed out when feature disabled -->
        <fieldset
          disabled={!settings.allowSubnetBypass?.enabled || store.isLoading || store.isSaving}
          class="contents"
          aria-describedby="subnet-bypass-status"
        >
          <span id="subnet-bypass-status" class="sr-only">
            {settings.allowSubnetBypass?.enabled
              ? t('settings.security.allowSubnetBypassLabel')
              : t('settings.security.bypassAuthentication.disabled')}
          </span>
          <div
            class="space-y-4 transition-opacity duration-200"
            class:opacity-50={!settings.allowSubnetBypass?.enabled}
          >
            <TextInput
              id="allowed-subnet"
              value={settings.allowSubnetBypass.subnet}
              label={t('settings.security.allowedSubnetsLabel')}
              placeholder={t('settings.security.placeholders.subnet')}
              helpText={t('settings.security.allowedSubnetsHelp')}
              disabled={!settings.allowSubnetBypass?.enabled ||
                store.isLoading ||
                store.isSaving}
              onchange={updateSubnetBypassSubnet}
            />

            <div class="flex items-start gap-3 p-4 rounded-lg bg-[color-mix(in_srgb,var(--color-warning)_15%,transparent)] text-[var(--color-warning)]">
              <TriangleAlert class="size-5 shrink-0" />
              <span>
                <strong>{t('settings.security.securityWarningTitle')}</strong>
                {t('settings.security.subnetWarningText')}
              </span>
            </div>
          </div>
        </fieldset>
      </div>
    </SettingsSection>
  </div>
{/snippet}

{#snippet terminalTabContent()}
  <div class="space-y-6">
    <SettingsSection title={t('settings.security.terminal.title')}>
      <div class="flex items-start gap-3 p-3 rounded-lg bg-[color-mix(in_srgb,var(--color-warning)_10%,transparent)] border border-[color-mix(in_srgb,var(--color-warning)_30%,transparent)] mb-4">
        <TriangleAlert class="size-5 text-[var(--color-warning)] shrink-0 mt-0.5" />
        <p class="text-sm text-[var(--color-base-content)] opacity-80">{t('settings.security.terminal.securityWarning')}</p>
      </div>
      <Checkbox
        label={t('settings.security.terminal.enableLabel')}
        helpText={t('settings.security.terminal.enableHelpText')}
        checked={enableTerminal}
        onchange={handleTerminalToggle}
        disabled={store.isLoading || store.isSaving}
      />
    </SettingsSection>
  </div>
{/snippet}

<!-- Main Content -->
<main class="settings-page-content" aria-label={t('settings.security.pageLabel')}>
  <SettingsTabs {tabs} bind:activeTab />
</main>
