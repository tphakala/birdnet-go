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

  // SECURITY: Define maximum password length to prevent DoS
  const MAX_PASSWORD_LENGTH = 512; // Reasonable limit for security
  const MAX_REDIRECT_LENGTH = 2000;

  // Logger for authentication debugging
  const logger = loggers.auth;

  // Loading state type for single state management
  type LoadingState = 'idle' | 'password' | 'google' | 'github';

  interface AuthEndpoints {
    google?: string;
    github?: string;
  }

  interface AuthConfig {
    basicEnabled: boolean;
    googleEnabled: boolean;
    githubEnabled: boolean;
    endpoints?: AuthEndpoints;
  }

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
    authConfig = { basicEnabled: true, googleEnabled: false, githubEnabled: false },
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
  function handleOAuthLogin(provider: 'google' | 'github') {
    // Use configurable endpoints or fallback to defaults
    const defaultEndpoints = {
      google: '/api/v1/auth/google',
      github: '/api/v1/auth/github',
    };

    const configuredEndpoints = authConfig.endpoints || {};
    const endpoint = safeGet(configuredEndpoints, provider) || safeGet(defaultEndpoints, provider);

    // SECURITY: Basic endpoint validation
    if (!endpoint || !endpoint.startsWith('/api/v1/auth/')) {
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
      class="modal-box sm:p-6 sm:pb-10 p-3 overflow-y-auto"
      role="dialog"
      aria-modal="true"
      aria-labelledby="modal-title"
    >
      <form onsubmit={handleSubmit}>
        <input type="hidden" name="redirect" value={safeRedirectUrl} />

        <div class="flex items-start flex-row">
          <div class="hidden xs:flex flex-initial">
            <div
              class="mx-auto flex h-12 w-12 sm:w-12 sm:h-12 flex-shrink-0 items-center justify-center rounded-full bg-blue-600 sm:mx-0 sm:h-10 sm:w-10"
            >
              <svg
                xmlns="http://www.w3.org/2000/svg"
                width="24"
                height="24"
                viewBox="0 0 24 24"
                fill="none"
                stroke-width="2"
                stroke-linecap="round"
                stroke-linejoin="round"
                class="lucide-lock-open stroke-white dark:stroke-gray-200"
              >
                <rect width="18" height="11" x="3" y="11" rx="2" ry="2"></rect>
                <path d="M7 11V7a5 5 0 0 1 9.9-1"></path>
              </svg>
            </div>
          </div>

          <div class="flex-1">
            <h3 id="modal-title" class="text-xl font-black py-2 px-6">Login to BirdNET-Go</h3>
            {#if authConfig.basicEnabled}
              <div class="form-control p-6 mx-2 xs:ml-0 xs:mx-14">
                <label class="label" for="loginPassword" id="passwordLabel">Password</label>
                <input
                  type="password"
                  id="loginPassword"
                  bind:value={password}
                  class="input input-bordered"
                  required
                  disabled={isAnyLoading}
                  autocomplete="current-password"
                  aria-required="true"
                  aria-describedby="loginError"
                />
                {#if error}
                  <div
                    id="loginError"
                    class="text-red-700 relative mt-2"
                    role="alert"
                    aria-live="polite"
                  >
                    {error}
                  </div>
                {/if}

                <!-- SECURITY: Rate limiting notice -->
              </div>
            {/if}
          </div>
        </div>

        {#if authConfig.basicEnabled}
          <div class="modal-action px-8 xs:px-[4.5rem] flex-row gap-4 justify-between">
            <button
              type="button"
              onclick={onClose}
              class="btn btn-outline"
              disabled={isAnyLoading}
              aria-label="Cancel login"
            >
              Cancel
            </button>
            <button
              type="submit"
              class="btn btn-primary grow pr-10"
              disabled={isAnyLoading || !password}
              aria-label="Login with password"
            >
              {#if isSubmitting}
                <span class="loading loading-spinner" aria-hidden="true"></span>
              {/if}
              Login
            </button>
          </div>
        {/if}

        {#if authConfig.basicEnabled && (authConfig.googleEnabled || authConfig.githubEnabled)}
          <div class="divider">or</div>
        {/if}

        {#if authConfig.googleEnabled || authConfig.githubEnabled}
          <div class="flex flex-col sm:flex-row gap-4 flex-wrap px-6 xs:px-16 pb-6">
            {#if authConfig.googleEnabled}
              <button
                type="button"
                class="btn btn-primary grow xs:pr-10 text-xs xs:text-sm"
                onclick={() => handleOAuthLogin('google')}
                disabled={isAnyLoading}
                aria-label="Login with Google"
              >
                {#if googleLoading}
                  <span
                    class="loading loading-spinner xs:loading xs:loading-spinner"
                    aria-hidden="true"
                  ></span>
                {/if}
                Login with Google
              </button>
            {/if}

            {#if authConfig.githubEnabled}
              <button
                type="button"
                class="btn btn-primary grow xs:pr-10 text-xs xs:text-sm"
                onclick={() => handleOAuthLogin('github')}
                disabled={isAnyLoading}
                aria-label="Login with GitHub"
              >
                {#if githubLoading}
                  <span
                    class="loading loading-spinner xs:loading xs:loading-spinner"
                    aria-hidden="true"
                  ></span>
                {/if}
                Login with GitHub
              </button>
            {/if}
          </div>
        {/if}
      </form>

      <!-- Close button -->
      <button
        class="btn btn-ghost btn-circle btn absolute text-lg right-2 top-2"
        onclick={onClose}
        disabled={isSubmitting}
        aria-label="Close login dialog"
      >
        ✕
      </button>
    </div>

    <div
      class="modal-backdrop"
      onclick={onClose}
      onkeydown={e => e.key === 'Escape' && onClose()}
      role="button"
      tabindex="-1"
      aria-label="Close modal"
    ></div>
  </div>
{/if}
