<!--
  SECURITY-HARDENED LoginModal Component

  Security improvements implemented:
  - Input validation and sanitization
  - Redirect URL validation
  - Secure error handling
  - Rate limiting protection
  - Secure state management
-->
<script lang="ts">
  import { api } from '$lib/utils/api';
  import { safeGet, safeArrayAccess, safeElementAccess } from '$lib/utils/security';
  import { extractRelativePath } from '$lib/utils/urlHelpers';
  import { loggers } from '$lib/utils/logger';
  import { t } from '$lib/i18n';
  import { X, ShieldCheck, KeyRound } from '@lucide/svelte';
  import { getEnabledProviders, getProvider } from '$lib/auth';
  import type { AuthConfig } from '../../../../app.d';

  // SECURITY: Define maximum password length to prevent DoS
  const MAX_PASSWORD_LENGTH = 512; // Reasonable limit for security
  const MAX_REDIRECT_LENGTH = 2000;

  // Logger for authentication debugging
  const logger = loggers.auth;

  // Loading state: 'idle', 'password', or a provider ID (e.g., 'google')
  type LoadingState = 'idle' | 'password' | string;

  interface Props {
    isOpen: boolean;
    onClose: () => void;
    redirectUrl?: string;
    authConfig?: AuthConfig;
  }

  let {
    isOpen = false,
    onClose,
    redirectUrl = '/ui/',
    authConfig = {
      basicEnabled: true,
      enabledProviders: [],
    },
  }: Props = $props();

  let password = $state('');
  let error = $state('');
  let loadingState = $state<LoadingState>('idle');

  // Compute safe redirect URL immediately
  let safeRedirectUrl = $derived(
    redirectUrl && validateRedirectUrl(redirectUrl) ? redirectUrl : detectBasePath()
  );

  // Get enabled OAuth providers from registry
  let enabledProviders = $derived(getEnabledProviders(authConfig.enabledProviders));
  let hasOAuthProviders = $derived(enabledProviders.length > 0);

  // Computed loading states for UI
  let isSubmitting = $derived(loadingState === 'password');
  let isAnyLoading = $derived(loadingState !== 'idle');

  // SECURITY: Validate redirect URL to prevent open redirects
  function validateRedirectUrl(url: string): boolean {
    try {
      // Only allow relative URLs starting with /
      if (!url.startsWith('/')) {
        return false;
      }

      // Prevent protocol-relative URLs
      if (url.startsWith('//')) {
        return false;
      }

      // Prevent javascript: or data: URLs
      if (url.toLowerCase().includes('javascript:') || url.toLowerCase().includes('data:')) {
        return false;
      }

      // Check length
      if (url.length > MAX_REDIRECT_LENGTH) {
        return false;
      }

      return true;
    } catch {
      return false;
    }
  }

  // SECURITY: Sanitize and validate password input
  function validatePassword(pwd: string): { isValid: boolean; error?: string } {
    if (!pwd) {
      return { isValid: false, error: 'Password is required' };
    }

    if (pwd.length > MAX_PASSWORD_LENGTH) {
      return { isValid: false, error: 'Password is too long' };
    }

    // Check for control characters (ASCII < 32) and other dangerous characters
    for (let i = 0; i < pwd.length; i++) {
      const charCode = pwd.charCodeAt(i);
      if (charCode < 32 && charCode !== 9) {
        // Allow tab (9) but reject other control chars
        return { isValid: false, error: 'Password contains invalid characters' };
      }
    }

    return { isValid: true };
  }

  // SECURITY: Extract current base path from window location
  function detectBasePath(): string {
    // Ensure we have a valid pathname (for tests and SSR)
    const pathname = window.location?.pathname || '/';

    // Common UI base paths to check
    const commonBasePaths = ['/ui/', '/app/', '/admin/', '/dashboard/'];

    for (const basePath of commonBasePaths) {
      if (pathname.startsWith(basePath)) {
        return basePath;
      }
    }

    // If no common base path found, check if we're in a subfolder
    const pathSegments = pathname.split('/').filter(Boolean);
    if (pathSegments.length > 0) {
      // Return the first segment as base path
      const firstSegment = safeArrayAccess(pathSegments, 0);
      return firstSegment ? `/${firstSegment}/` : '/';
    }

    // Default to root
    return '/';
  }

  async function handlePasswordLogin() {
    if (loadingState !== 'idle') {
      return;
    }

    // SECURITY: Validate password after trimming (to match what will be sent)
    const trimmedPassword = password.trim();
    const passwordValidation = validatePassword(trimmedPassword);
    if (!passwordValidation.isValid) {
      error = passwordValidation.error || 'Invalid input';
      return;
    }

    // SECURITY: Detect current base path
    const currentBasePath = detectBasePath();

    error = '';
    loadingState = 'password';

    // Extract relative path to avoid backend duplication
    const finalRedirectUrl = extractRelativePath(safeRedirectUrl, currentBasePath);

    // Debug logging for troubleshooting redirect issues
    logger.debug('Login redirect path extraction', {
      original: safeRedirectUrl,
      basePath: currentBasePath,
      extracted: finalRedirectUrl,
      component: 'LoginModal',
      action: 'handlePasswordLogin',
    });

    const loginPayload = {
      username: 'birdnet-client', // Must match Security.BasicAuth.ClientID in config
      password: trimmedPassword, // Use the already trimmed password
      redirectUrl: finalRedirectUrl, // Pass the relative redirect URL to avoid duplication
      basePath: currentBasePath, // Send the detected base path
    };

    try {
      // SECURITY: Don't update auth state until server confirms success
      // NOTE: Backend expects username to match Security.BasicAuth.ClientID (default: "birdnet-client")
      const response = await api.post<{
        success: boolean;
        message: string;
        redirectUrl?: string;
      }>('/api/v2/auth/login', loginPayload);

      // Check if we need to complete OAuth flow
      if (response.redirectUrl) {
        logger.debug('OAuth callback redirect received', {
          callbackUrl: response.redirectUrl,
          originalRedirect: finalRedirectUrl,
          component: 'LoginModal',
          action: 'handlePasswordLogin',
        });

        // Backend returned OAuth callback URL to complete authentication
        // Redirect immediately to complete the OAuth flow
        window.location.href = response.redirectUrl;
        return; // Exit early - OAuth callback will handle the rest
      }

      // If no redirectUrl, try a simple page refresh to trigger auth state update

      // Close modal first
      onClose();

      // Give a moment for modal to close, then refresh
      setTimeout(() => {
        window.location.reload();
      }, 500);
    } catch {
      error = 'Invalid credentials. Please try again.';
    } finally {
      loadingState = 'idle';
    }
  }

  // SECURITY: Validate OAuth endpoints before redirect
  function handleOAuthLogin(providerId: string) {
    // Get provider from registry
    const provider = getProvider(providerId);
    if (!provider) {
      error = 'Unknown authentication provider.';
      return;
    }

    // Use endpoint from registry, with optional override from config
    const configuredEndpoints = authConfig.endpoints || {};
    const endpoint = safeGet(configuredEndpoints, providerId) || provider.authEndpoint;

    // SECURITY: Basic endpoint validation - accept OAuth and API v2 auth formats
    if (!endpoint || (!endpoint.startsWith('/auth/') && !endpoint.startsWith('/api/v2/auth/'))) {
      error = 'Configuration error. Please contact your administrator.';
      return;
    }

    loadingState = providerId;

    // Debug logging for OAuth flow
    logger.debug('OAuth login initiated', {
      provider: providerId,
      endpoint,
      currentPath: window.location.pathname,
      component: 'LoginModal',
      action: 'handleOAuthLogin',
    });

    // Redirect to OAuth provider
    window.location.href = endpoint;
  }

  function handleSubmit(event: Event) {
    event.preventDefault();
    handlePasswordLogin();
  }

  // Focus trap for accessibility
  // svelte-ignore non_reactive_update
  let modalElement: HTMLElement;
  let focusTrap: (() => void) | null = null;

  // SECURITY: Secure state cleanup and focus management
  $effect(() => {
    if (isOpen && modalElement) {
      // Create focus trap
      const focusableElements = modalElement.querySelectorAll(
        'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])'
      );
      const firstElement = safeElementAccess<HTMLElement>(focusableElements, 0, HTMLElement);
      const lastElement = safeElementAccess<HTMLElement>(
        focusableElements,
        focusableElements.length - 1,
        HTMLElement
      );

      const trapFocus = (e: KeyboardEvent) => {
        if (e.key === 'Tab' && firstElement && lastElement) {
          if (e.shiftKey) {
            if (document.activeElement === firstElement) {
              e.preventDefault();
              lastElement.focus();
            }
          } else {
            if (document.activeElement === lastElement) {
              e.preventDefault();
              firstElement.focus();
            }
          }
        } else if (e.key === 'Escape') {
          e.preventDefault();
          onClose();
        }
      };

      modalElement.addEventListener('keydown', trapFocus);
      firstElement?.focus();

      focusTrap = () => {
        modalElement.removeEventListener('keydown', trapFocus);
      };
    } else if (!isOpen) {
      // Clear all sensitive state when modal closes
      password = '';
      error = '';
      loadingState = 'idle';

      // Clean up focus trap
      if (focusTrap) {
        focusTrap();
        focusTrap = null;
      }
    }

    return () => {
      if (focusTrap) {
        focusTrap();
        focusTrap = null;
      }
    };
  });
</script>

{#if isOpen}
  <div class="modal modal-open">
    <div
      bind:this={modalElement}
      class="modal-box max-w-md p-8 overflow-y-auto"
      role="dialog"
      aria-modal="true"
      aria-labelledby="modal-title"
    >
      <!-- Close button -->
      <button
        type="button"
        class="btn btn-sm btn-circle btn-ghost absolute right-3 top-3"
        onclick={onClose}
        disabled={isAnyLoading}
        aria-label="Close login dialog"
      >
        <X class="size-4" />
      </button>

      <form onsubmit={handleSubmit} class="space-y-6">
        <input type="hidden" name="redirect" value={safeRedirectUrl} />

        <!-- Header -->
        <div class="text-center space-y-2">
          <div class="flex justify-center mb-4">
            <div class="p-3 bg-primary/10 rounded-full">
              <ShieldCheck class="size-8 text-primary" />
            </div>
          </div>
          <h3 id="modal-title" class="text-2xl font-semibold">
            {t('auth.loginTitle')}
          </h3>
          <p class="text-base-content/60 text-sm">
            {t('auth.loginSubtitle')}
          </p>
        </div>

        <!-- Password login section -->
        {#if authConfig.basicEnabled}
          <div class="space-y-4">
            <div class="form-control">
              <label class="label" for="loginPassword">
                <span class="label-text font-medium">{t('auth.password')}</span>
              </label>
              <div class="relative">
                <KeyRound
                  class="absolute left-3 top-1/2 -translate-y-1/2 size-5 text-base-content/40"
                />
                <input
                  type="password"
                  id="loginPassword"
                  bind:value={password}
                  class="input input-bordered w-full pl-11"
                  placeholder={t('auth.enterPassword')}
                  required
                  disabled={isAnyLoading}
                  autocomplete="current-password"
                  aria-required="true"
                  aria-describedby={error ? 'loginError' : undefined}
                />
              </div>
              {#if error}
                <div
                  id="loginError"
                  class="text-error text-sm mt-2"
                  role="alert"
                  aria-live="polite"
                >
                  {error}
                </div>
              {/if}
            </div>

            <button
              type="submit"
              class="btn btn-primary w-full"
              disabled={isAnyLoading || !password}
              aria-label="Continue with password"
            >
              {#if isSubmitting}
                <span class="loading loading-spinner loading-sm" aria-hidden="true"></span>
              {/if}
              {t('auth.continue')}
            </button>
          </div>
        {/if}

        <!-- Divider -->
        {#if authConfig.basicEnabled && hasOAuthProviders}
          <div class="divider text-base-content/40 text-xs uppercase">{t('auth.or')}</div>
        {/if}

        <!-- OAuth providers -->
        {#if hasOAuthProviders}
          <div class="space-y-3">
            {#each enabledProviders as provider (provider.id)}
              {@const Icon = provider.icon}
              {@const isLoading = loadingState === provider.id}
              <button
                type="button"
                class="btn btn-outline w-full justify-start gap-3 font-normal hover:bg-base-200"
                onclick={() => handleOAuthLogin(provider.id)}
                disabled={isAnyLoading}
                aria-label={t(provider.loginButtonKey)}
              >
                {#if isLoading}
                  <span class="loading loading-spinner loading-sm" aria-hidden="true"></span>
                {:else}
                  <Icon class="size-5 shrink-0" />
                {/if}
                <span class="flex-1 text-left">{t(provider.loginButtonKey)}</span>
              </button>
            {/each}
          </div>
        {/if}
      </form>
    </div>

    <div
      class="modal-backdrop bg-black/50"
      onclick={onClose}
      onkeydown={e => e.key === 'Escape' && onClose()}
      role="button"
      tabindex="-1"
      aria-label="Close modal"
    ></div>
  </div>
{/if}
