<!--
  Security Settings Page Component
  
  Purpose: Configure authentication and access control for BirdNET-Go including
  HTTPS/TLS settings, basic authentication, OAuth2 social login providers, and
  subnet-based authentication bypass.
  
  Features:
  - Server configuration with automatic TLS via Let's Encrypt
  - Basic authentication with password protection
  - OAuth2 integration (Google, GitHub) with user restrictions
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
  import SettingsSection from '$lib/desktop/features/settings/components/SettingsSection.svelte';
  import SettingsNote from '$lib/desktop/features/settings/components/SettingsNote.svelte';
  import SettingsTabs from '$lib/desktop/features/settings/components/SettingsTabs.svelte';
  import type { TabDefinition } from '$lib/desktop/features/settings/components/SettingsTabs.svelte';
  import {
    settingsStore,
    settingsActions,
    securitySettings,
  } from '$lib/stores/settings';
  import { hasSettingsChanged } from '$lib/utils/settingsChanges';
  import { TriangleAlert, ExternalLink, Server, KeyRound, Users, Network } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { GoogleIcon, GithubIcon, MicrosoftIcon, getProvider } from '$lib/auth';


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
      googleAuth: {
        enabled: false,
        clientId: '',
        clientSecret: '',
        userId: '',
      },
      githubAuth: {
        enabled: false,
        clientId: '',
        clientSecret: '',
        userId: '',
      },
      microsoftAuth: {
        enabled: false,
        clientId: '',
        clientSecret: '',
        userId: '',
      },
      allowSubnetBypass: {
        enabled: false,
        subnet: '',
      },
    }
  );

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
      {
        googleAuth: store.originalData.security?.googleAuth,
        githubAuth: store.originalData.security?.githubAuth,
        microsoftAuth: store.originalData.security?.microsoftAuth,
      },
      {
        googleAuth: store.formData.security?.googleAuth,
        githubAuth: store.formData.security?.githubAuth,
        microsoftAuth: store.formData.security?.microsoftAuth,
      }
    )
  );

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

  // Use OAuth callback paths from provider registry for consistency
  // Backend supports both /auth/:provider/callback and /api/v1/auth/:provider/callback
  let googleRedirectURI = $derived(`${currentHost}${getProvider('google')?.settings.callbackPath ?? '/auth/google/callback'}`);
  let githubRedirectURI = $derived(`${currentHost}${getProvider('github')?.settings.callbackPath ?? '/auth/github/callback'}`);
  let microsoftRedirectURI = $derived(`${currentHost}${getProvider('microsoft')?.settings.callbackPath ?? '/auth/microsoftonline/callback'}`);

  // Computed redirect URIs for saving to config (based on configured host/baseUrl)
  let googleConfigRedirectURI = $derived(`${configuredBaseUrl}${getProvider('google')?.settings.callbackPath ?? '/auth/google/callback'}`);
  let githubConfigRedirectURI = $derived(`${configuredBaseUrl}${getProvider('github')?.settings.callbackPath ?? '/auth/github/callback'}`);
  let microsoftConfigRedirectURI = $derived(`${configuredBaseUrl}${getProvider('microsoft')?.settings.callbackPath ?? '/auth/microsoftonline/callback'}`);

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

  // Helper to build OAuth update object, handling redirectURI correctly:
  // - When hasExplicitBaseUrl is true: use computed redirectURI
  // - When false: explicitly remove redirectURI so backend auto-generates it
  // This prevents stale redirectURI values from persisting when baseUrl is removed
  function buildOAuthUpdate<T extends { redirectURI?: string }>(
    currentSettings: T,
    updates: Partial<T>,
    computedRedirectURI: string
  ): T {
    const result = { ...currentSettings, ...updates };
    if (hasExplicitBaseUrl) {
      result.redirectURI = computedRedirectURI;
    } else {
      delete result.redirectURI;
    }
    return result;
  }

  // Google OAuth update handlers
  function updateGoogleAuthEnabled(enabled: boolean) {
    const googleUpdate = buildOAuthUpdate(settings.googleAuth, { enabled }, googleConfigRedirectURI);
    settingsActions.updateSection('security', { ...settings, googleAuth: googleUpdate });
  }

  function updateGoogleClientId(clientId: string) {
    const googleUpdate = buildOAuthUpdate(settings.googleAuth, { clientId }, googleConfigRedirectURI);
    settingsActions.updateSection('security', { ...settings, googleAuth: googleUpdate });
  }

  function updateGoogleClientSecret(clientSecret: string) {
    const googleUpdate = buildOAuthUpdate(
      settings.googleAuth,
      { clientSecret },
      googleConfigRedirectURI
    );
    settingsActions.updateSection('security', { ...settings, googleAuth: googleUpdate });
  }

  function updateGoogleUserId(userId: string) {
    const googleUpdate = buildOAuthUpdate(settings.googleAuth, { userId }, googleConfigRedirectURI);
    settingsActions.updateSection('security', { ...settings, googleAuth: googleUpdate });
  }

  // GitHub OAuth update handlers
  function updateGithubAuthEnabled(enabled: boolean) {
    const githubUpdate = buildOAuthUpdate(settings.githubAuth, { enabled }, githubConfigRedirectURI);
    settingsActions.updateSection('security', { ...settings, githubAuth: githubUpdate });
  }

  function updateGithubClientId(clientId: string) {
    const githubUpdate = buildOAuthUpdate(settings.githubAuth, { clientId }, githubConfigRedirectURI);
    settingsActions.updateSection('security', { ...settings, githubAuth: githubUpdate });
  }

  function updateGithubClientSecret(clientSecret: string) {
    const githubUpdate = buildOAuthUpdate(
      settings.githubAuth,
      { clientSecret },
      githubConfigRedirectURI
    );
    settingsActions.updateSection('security', { ...settings, githubAuth: githubUpdate });
  }

  function updateGithubUserId(userId: string) {
    const githubUpdate = buildOAuthUpdate(settings.githubAuth, { userId }, githubConfigRedirectURI);
    settingsActions.updateSection('security', { ...settings, githubAuth: githubUpdate });
  }

  // Microsoft OAuth update handlers
  function updateMicrosoftAuth(update: Partial<typeof settings.microsoftAuth>) {
    const microsoftUpdate = buildOAuthUpdate(settings.microsoftAuth, update, microsoftConfigRedirectURI);
    settingsActions.updateSection('security', { ...settings, microsoftAuth: microsoftUpdate });
  }

  function updateMicrosoftAuthEnabled(enabled: boolean) {
    updateMicrosoftAuth({ enabled });
  }

  function updateMicrosoftClientId(clientId: string) {
    updateMicrosoftAuth({ clientId });
  }

  function updateMicrosoftClientSecret(clientSecret: string) {
    updateMicrosoftAuth({ clientSecret });
  }

  function updateMicrosoftUserId(userId: string) {
    updateMicrosoftAuth({ userId });
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

      <div class="border-t border-base-300 pt-4 mt-4">
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
      originalData={{
        googleAuth: store.originalData.security?.googleAuth,
        githubAuth: store.originalData.security?.githubAuth,
        microsoftAuth: store.originalData.security?.microsoftAuth,
      }}
      currentData={{
        googleAuth: store.formData.security?.googleAuth,
        githubAuth: store.formData.security?.githubAuth,
        microsoftAuth: store.formData.security?.microsoftAuth,
      }}
    >
    <div class="space-y-6">
      <!-- Google Auth -->
      <div class="border border-base-300 rounded-lg p-4">
        <h4 class="text-lg font-medium mb-4 flex items-center gap-3">
          <GoogleIcon class="w-6 h-6" />
          {t('settings.security.oauth.google.title')}
        </h4>

        <Checkbox
          checked={settings.googleAuth.enabled}
          label={t('settings.security.oauth.google.enableLabel')}
          disabled={store.isLoading || store.isSaving}
          onchange={updateGoogleAuthEnabled}
        />

        {#if settings.googleAuth?.enabled}
          <form class="mt-4 space-y-4" onsubmit={(e) => e.preventDefault()} autocomplete="off">
            <!-- Redirect URI Information -->
            <div class="bg-base-200 p-3 rounded-lg">
              <div class="text-sm">
                <p class="font-medium mb-1">{t('settings.security.oauth.google.redirectUriTitle')}</p>
                <code class="text-xs bg-base-300 px-2 py-1 rounded-sm">{googleRedirectURI}</code>
              </div>
              <a
                href={getProvider('google')?.settings.credentialsUrl}
                target="_blank"
                rel="noopener"
                class="text-sm text-primary hover:text-primary-focus inline-flex items-center gap-1 mt-2"
              >
                {t('settings.security.oauth.google.getCredentialsLabel')}
                <ExternalLink class="size-4" />
              </a>
            </div>

            <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
              <PasswordField
                label={t('settings.security.oauth.google.clientIdLabel')}
                value={settings.googleAuth.clientId}
                onUpdate={updateGoogleClientId}
                placeholder=""
                helpText={t('settings.security.oauth.google.clientIdHelpText')}
                disabled={store.isLoading || store.isSaving}
                allowReveal={true}
              />

              <PasswordField
                label={t('settings.security.oauth.google.clientSecretLabel')}
                value={settings.googleAuth.clientSecret}
                onUpdate={updateGoogleClientSecret}
                placeholder=""
                helpText={t('settings.security.oauth.google.clientSecretHelpText')}
                disabled={store.isLoading || store.isSaving}
                allowReveal={true}
              />
            </div>

            <TextInput
              id="google-user-id"
              value={settings.googleAuth.userId ?? ''}
              label={t('settings.security.oauth.google.userIdLabel')}
              placeholder={t('settings.security.placeholders.allowedUsers')}
              disabled={store.isLoading || store.isSaving}
              onchange={updateGoogleUserId}
            />
          </form>
        {/if}
      </div>

      <!-- GitHub Auth -->
      <div class="border border-base-300 rounded-lg p-4">
        <h4 class="text-lg font-medium mb-4 flex items-center gap-3">
          <GithubIcon class="w-6 h-6" />
          {t('settings.security.oauth.github.title')}
        </h4>

        <Checkbox
          checked={settings.githubAuth.enabled}
          label={t('settings.security.oauth.github.enableLabel')}
          disabled={store.isLoading || store.isSaving}
          onchange={updateGithubAuthEnabled}
        />

        {#if settings.githubAuth?.enabled}
          <form class="mt-4 space-y-4" onsubmit={(e) => e.preventDefault()} autocomplete="off">
            <!-- Redirect URI Information -->
            <div class="bg-base-200 p-3 rounded-lg">
              <div class="text-sm">
                <p class="font-medium mb-1">{t('settings.security.oauth.github.redirectUriTitle')}</p>
                <code class="text-xs bg-base-300 px-2 py-1 rounded-sm">{githubRedirectURI}</code>
              </div>
              <a
                href={getProvider('github')?.settings.credentialsUrl}
                target="_blank"
                rel="noopener"
                class="text-sm text-primary hover:text-primary-focus inline-flex items-center gap-1 mt-2"
              >
                {t('settings.security.oauth.github.getCredentialsLabel')}
                <ExternalLink class="size-4" />
              </a>
            </div>

            <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
              <PasswordField
                label={t('settings.security.oauth.github.clientIdLabel')}
                value={settings.githubAuth.clientId}
                onUpdate={updateGithubClientId}
                placeholder=""
                helpText={t('settings.security.oauth.github.clientIdHelpText')}
                disabled={store.isLoading || store.isSaving}
                allowReveal={true}
              />

              <PasswordField
                label={t('settings.security.oauth.github.clientSecretLabel')}
                value={settings.githubAuth.clientSecret}
                onUpdate={updateGithubClientSecret}
                placeholder=""
                helpText={t('settings.security.oauth.github.clientSecretHelpText')}
                disabled={store.isLoading || store.isSaving}
                allowReveal={true}
              />
            </div>

            <TextInput
              id="github-user-id"
              value={settings.githubAuth.userId ?? ''}
              label={t('settings.security.oauth.github.userIdLabel')}
              placeholder={t('settings.security.placeholders.allowedUsers')}
              disabled={store.isLoading || store.isSaving}
              onchange={updateGithubUserId}
            />
          </form>
        {/if}
      </div>

      <!-- Microsoft Auth -->
      <div class="border border-base-300 rounded-lg p-4">
        <h4 class="text-lg font-medium mb-4 flex items-center gap-3">
          <MicrosoftIcon class="w-6 h-6" />
          {t('settings.security.oauth.microsoft.title')}
        </h4>

        <Checkbox
          checked={settings.microsoftAuth.enabled}
          label={t('settings.security.oauth.microsoft.enableLabel')}
          disabled={store.isLoading || store.isSaving}
          onchange={updateMicrosoftAuthEnabled}
        />

        {#if settings.microsoftAuth?.enabled}
          <form class="mt-4 space-y-4" onsubmit={(e) => e.preventDefault()} autocomplete="off">
            <!-- Redirect URI Information -->
            <div class="bg-base-200 p-3 rounded-lg">
              <div class="text-sm">
                <p class="font-medium mb-1">{t('settings.security.oauth.microsoft.redirectUriTitle')}</p>
                <code class="text-xs bg-base-300 px-2 py-1 rounded-sm">{microsoftRedirectURI}</code>
              </div>
              <a
                href={getProvider('microsoft')?.settings.credentialsUrl}
                target="_blank"
                rel="noopener noreferrer"
                class="text-sm text-primary hover:text-primary-focus inline-flex items-center gap-1 mt-2"
              >
                {t('settings.security.oauth.microsoft.getCredentialsLabel')}
                <ExternalLink class="size-4" />
              </a>
            </div>

            <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
              <PasswordField
                label={t('settings.security.oauth.microsoft.clientIdLabel')}
                value={settings.microsoftAuth.clientId}
                onUpdate={updateMicrosoftClientId}
                placeholder=""
                helpText={t('settings.security.oauth.microsoft.clientIdHelpText')}
                disabled={store.isLoading || store.isSaving}
                allowReveal={true}
              />

              <PasswordField
                label={t('settings.security.oauth.microsoft.clientSecretLabel')}
                value={settings.microsoftAuth.clientSecret}
                onUpdate={updateMicrosoftClientSecret}
                placeholder=""
                helpText={t('settings.security.oauth.microsoft.clientSecretHelpText')}
                disabled={store.isLoading || store.isSaving}
                allowReveal={true}
              />
            </div>

            <TextInput
              id="microsoft-user-id"
              value={settings.microsoftAuth.userId ?? ''}
              label={t('settings.security.oauth.microsoft.userIdLabel')}
              placeholder={t('settings.security.placeholders.allowedUsers')}
              disabled={store.isLoading || store.isSaving}
              onchange={updateMicrosoftUserId}
            />
          </form>
        {/if}
      </div>
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

            <div class="alert alert-warning">
              <TriangleAlert class="size-5" />
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

<!-- Main Content -->
<main class="settings-page-content" aria-label="Security settings configuration">
  <SettingsTabs {tabs} bind:activeTab />
</main>
