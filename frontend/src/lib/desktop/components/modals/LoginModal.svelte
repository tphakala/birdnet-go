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
  import type { AuthConfig } from '../../../../app.d';

  // SECURITY: Define maximum password length to prevent DoS
  const MAX_PASSWORD_LENGTH = 512; // Reasonable limit for security
  const MAX_REDIRECT_LENGTH = 2000;

  // Logger for authentication debugging
  const logger = loggers.auth;

  // Loading state type for single state management
  type LoadingState = 'idle' | 'password' | 'google' | 'github' | 'microsoft';

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
      googleEnabled: false,
      githubEnabled: false,
      microsoftEnabled: false,
    },
  }: Props = $props();

  let password = $state('');
  let error = $state('');
  let loadingState = $state<LoadingState>('idle');

  // Compute safe redirect URL immediately
  let safeRedirectUrl = $derived(
    redirectUrl && validateRedirectUrl(redirectUrl) ? redirectUrl : detectBasePath()
  );

  // Computed loading states for UI
  let isSubmitting = $derived(loadingState === 'password');
  let googleLoading = $derived(loadingState === 'google');
  let githubLoading = $derived(loadingState === 'github');
  let microsoftLoading = $derived(loadingState === 'microsoft');
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
  function handleOAuthLogin(provider: 'google' | 'github' | 'microsoft') {
    // Use clean OAuth routes (without /api/v1 prefix) for consistency
    // Backend supports both /auth/:provider and /api/v1/auth/:provider
    const defaultEndpoints = {
      google: '/auth/google',
      github: '/auth/github',
      microsoft: '/auth/microsoftonline',
    };

    const configuredEndpoints = authConfig.endpoints || {};
    const endpoint = safeGet(configuredEndpoints, provider) || safeGet(defaultEndpoints, provider);

    // SECURITY: Basic endpoint validation - accept both formats
    if (!endpoint || (!endpoint.startsWith('/auth/') && !endpoint.startsWith('/api/v1/auth/'))) {
      error = 'Configuration error. Please contact your administrator.';
      return;
    }

    loadingState = provider;

    // Debug logging for OAuth flow
    logger.debug('OAuth login initiated', {
      provider,
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
        {#if authConfig.basicEnabled && (authConfig.googleEnabled || authConfig.githubEnabled || authConfig.microsoftEnabled)}
          <div class="divider text-base-content/40 text-xs uppercase">{t('auth.or')}</div>
        {/if}

        <!-- OAuth providers -->
        {#if authConfig.googleEnabled || authConfig.githubEnabled || authConfig.microsoftEnabled}
          <div class="space-y-3">
            {#if authConfig.googleEnabled}
              <button
                type="button"
                class="btn btn-outline w-full justify-start gap-3 font-normal hover:bg-base-200"
                onclick={() => handleOAuthLogin('google')}
                disabled={isAnyLoading}
                aria-label={t('auth.continueWithGoogle')}
              >
                {#if googleLoading}
                  <span class="loading loading-spinner loading-sm" aria-hidden="true"></span>
                {:else}
                  <!-- Google Icon -->
                  <svg class="size-5 shrink-0" viewBox="0 0 24 24">
                    <path
                      fill="#4285F4"
                      d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92c-.26 1.37-1.04 2.53-2.21 3.31v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.09z"
                    />
                    <path
                      fill="#34A853"
                      d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z"
                    />
                    <path
                      fill="#FBBC05"
                      d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.07H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.93l2.85-2.22.81-.62z"
                    />
                    <path
                      fill="#EA4335"
                      d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.07l3.66 2.84c.87-2.6 3.3-4.53 6.16-4.53z"
                    />
                  </svg>
                {/if}
                <span class="flex-1 text-left">{t('auth.continueWithGoogle')}</span>
              </button>
            {/if}

            {#if authConfig.githubEnabled}
              <button
                type="button"
                class="btn btn-outline w-full justify-start gap-3 font-normal hover:bg-base-200"
                onclick={() => handleOAuthLogin('github')}
                disabled={isAnyLoading}
                aria-label={t('auth.continueWithGithub')}
              >
                {#if githubLoading}
                  <span class="loading loading-spinner loading-sm" aria-hidden="true"></span>
                {:else}
                  <!-- GitHub Icon -->
                  <svg class="size-5 shrink-0" fill="currentColor" viewBox="0 0 24 24">
                    <path
                      d="M12 0C5.374 0 0 5.373 0 12 0 17.302 3.438 21.8 8.207 23.387c.599.111.793-.261.793-.577v-2.234c-3.338.726-4.033-1.416-4.033-1.416-.546-1.387-1.333-1.756-1.333-1.756-1.089-.745.083-.729.083-.729 1.205.084 1.839 1.237 1.839 1.237 1.07 1.834 2.807 1.304 3.492.997.107-.775.418-1.305.762-1.604-2.665-.305-5.467-1.334-5.467-5.931 0-1.311.469-2.381 1.236-3.221-.124-.303-.535-1.524.117-3.176 0 0 1.008-.322 3.301 1.23A11.509 11.509 0 0112 5.803c1.02.005 2.047.138 3.006.404 2.291-1.552 3.297-1.23 3.297-1.23.653 1.653.242 2.874.118 3.176.77.84 1.235 1.911 1.235 3.221 0 4.609-2.807 5.624-5.479 5.921.43.372.823 1.102.823 2.222v3.293c0 .319.192.694.801.576C20.566 21.797 24 17.3 24 12c0-6.627-5.373-12-12-12z"
                    />
                  </svg>
                {/if}
                <span class="flex-1 text-left">{t('auth.continueWithGithub')}</span>
              </button>
            {/if}

            {#if authConfig.microsoftEnabled}
              <button
                type="button"
                class="btn btn-outline w-full justify-start gap-3 font-normal hover:bg-base-200"
                onclick={() => handleOAuthLogin('microsoft')}
                disabled={isAnyLoading}
                aria-label={t('auth.continueWithMicrosoft')}
              >
                {#if microsoftLoading}
                  <span class="loading loading-spinner loading-sm" aria-hidden="true"></span>
                {:else}
                  <!-- Microsoft Icon -->
                  <svg class="size-5 shrink-0" viewBox="0 0 24 24">
                    <path fill="#F25022" d="M1 1h10v10H1z" />
                    <path fill="#7FBA00" d="M13 1h10v10H13z" />
                    <path fill="#00A4EF" d="M1 13h10v10H1z" />
                    <path fill="#FFB900" d="M13 13h10v10H13z" />
                  </svg>
                {/if}
                <span class="flex-1 text-left">{t('auth.continueWithMicrosoft')}</span>
              </button>
            {/if}
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
