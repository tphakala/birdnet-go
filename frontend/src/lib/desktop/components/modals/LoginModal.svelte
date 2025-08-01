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
  import { t } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';

  const logger = loggers.auth;

  // SECURITY: Define maximum password length to prevent DoS
  const MAX_PASSWORD_LENGTH = 1000;
  const MAX_REDIRECT_LENGTH = 2000;

  // SECURITY: Rate limiting for login attempts
  const MAX_ATTEMPTS = 5;
  const RATE_LIMIT_MINUTES = 15;

  interface AuthConfig {
    basicEnabled: boolean;
    googleEnabled: boolean;
    githubEnabled: boolean;
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
  let isSubmitting = $state(false);
  let googleLoading = $state(false);
  let githubLoading = $state(false);

  // SECURITY: Rate limiting state
  let attemptCount = $state(0);
  let lastAttemptTime = $state(0);
  let isRateLimited = $state(false);

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
      return { isValid: false, error: t('auth.errors.passwordRequired') };
    }

    if (pwd.length > MAX_PASSWORD_LENGTH) {
      return { isValid: false, error: t('auth.errors.passwordTooLong') };
    }

    // Check for null bytes or other dangerous characters
    if (pwd.includes('\0') || pwd.includes('\r') || pwd.includes('\n')) {
      return { isValid: false, error: t('auth.errors.invalidCharacters') };
    }

    return { isValid: true };
  }

  // SECURITY: Check rate limiting
  function checkRateLimit(): boolean {
    const now = Date.now();
    const timeSinceLastAttempt = now - lastAttemptTime;

    // Reset attempts after rate limit period
    if (timeSinceLastAttempt > RATE_LIMIT_MINUTES * 60 * 1000) {
      attemptCount = 0;
      isRateLimited = false;
    }

    if (attemptCount >= MAX_ATTEMPTS) {
      isRateLimited = true;
      return false;
    }

    return true;
  }

  // SECURITY: Extract current base path from window location
  function detectBasePath(): string {
    const pathname = window.location.pathname;

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
      return `/${pathSegments[0]}/`;
    }

    // Default to root
    return '/';
  }

  async function handlePasswordLogin() {
    logger.info('[LOGIN_DEBUG] Starting login process', {
      hasPassword: !!password,
      passwordLength: password.length,
      redirectUrl,
      isSubmitting,
      attemptCount,
      isRateLimited,
    });

    if (isSubmitting) {
      logger.warn('[LOGIN_DEBUG] Login already in progress, skipping');
      return;
    }

    // SECURITY: Check rate limiting first
    if (!checkRateLimit()) {
      logger.warn('[LOGIN_DEBUG] Rate limit exceeded', { attemptCount, lastAttemptTime });
      error = t('auth.errors.rateLimited', { minutes: RATE_LIMIT_MINUTES });
      return;
    }

    // SECURITY: Validate password
    const passwordValidation = validatePassword(password);
    if (!passwordValidation.isValid) {
      logger.warn('[LOGIN_DEBUG] Password validation failed', { error: passwordValidation.error });
      error = passwordValidation.error || t('auth.errors.invalidInput');
      return;
    }

    // SECURITY: Detect current base path
    const currentBasePath = detectBasePath();
    logger.info('[LOGIN_DEBUG] Detected base path', {
      currentBasePath,
      windowLocation: window.location.href,
    });

    // SECURITY: Validate redirect URL
    const safeRedirectUrl =
      redirectUrl && validateRedirectUrl(redirectUrl) ? redirectUrl : currentBasePath;
    logger.info('[LOGIN_DEBUG] Redirect URL validation', {
      originalRedirectUrl: redirectUrl,
      isValid: redirectUrl ? validateRedirectUrl(redirectUrl) : 'N/A',
      safeRedirectUrl,
      currentBasePath,
    });

    error = '';
    isSubmitting = true;
    lastAttemptTime = Date.now();
    attemptCount += 1;

    const loginPayload = {
      username: 'birdnet-client', // Must match Security.BasicAuth.ClientID in config
      password: password.trim(), // Remove whitespace
      redirectUrl: safeRedirectUrl, // Pass the intended redirect URL
      basePath: currentBasePath, // Send the detected base path
    };

    logger.info('[LOGIN_DEBUG] Sending login request', {
      ...loginPayload,
      password: '[REDACTED]', // Don't log actual password
      timestamp: new Date().toISOString(),
    });

    try {
      // SECURITY: Don't update auth state until server confirms success
      // NOTE: Backend expects username to match Security.BasicAuth.ClientID (default: "birdnet-client")
      const response = await api.post<{
        success: boolean;
        message: string;
        redirectUrl?: string;
      }>('/api/v2/auth/login', loginPayload);

      logger.info('[LOGIN_DEBUG] Login response received', {
        success: response.success,
        message: response.message,
        hasRedirectUrl: !!response.redirectUrl,
        redirectUrl: response.redirectUrl,
        timestamp: new Date().toISOString(),
        responseKeys: Object.keys(response),
        fullResponse: JSON.stringify(response, null, 2),
      });

      // Check if we need to complete OAuth flow
      if (response.redirectUrl) {
        // Backend returned OAuth callback URL to complete authentication
        logger.info('[LOGIN_DEBUG] Redirecting to complete OAuth flow', {
          redirectUrl: response.redirectUrl,
          currentUrl: window.location.href,
          timestamp: new Date().toISOString(),
        });

        // SECURITY: Reset rate limiting on successful login attempt
        attemptCount = 0;
        isRateLimited = false;

        // Redirect immediately to complete the OAuth flow
        logger.info('[LOGIN_DEBUG] Performing redirect now');
        window.location.href = response.redirectUrl;
        return; // Exit early - OAuth callback will handle the rest
      }

      // If no redirectUrl, the backend might not have generated auth code properly
      // Let's try a simple page refresh to trigger auth state update
      logger.warn('[LOGIN_DEBUG] No redirectUrl in login response - trying page refresh', {
        response,
        currentUrl: window.location.href,
        timestamp: new Date().toISOString(),
      });

      // SECURITY: Reset rate limiting on successful login attempt
      attemptCount = 0;
      isRateLimited = false;

      // Close modal first
      logger.info('[LOGIN_DEBUG] Closing modal before refresh');
      onClose();

      // Give a moment for modal to close, then refresh
      logger.info('[LOGIN_DEBUG] Scheduling page refresh in 500ms');
      setTimeout(() => {
        logger.info('[LOGIN_DEBUG] Performing page refresh now', {
          currentUrl: window.location.href,
          timestamp: new Date().toISOString(),
        });
        window.location.reload();
      }, 500);
    } catch (err: unknown) {
      logger.error('[LOGIN_DEBUG] Login request failed', {
        error: err,
        errorMessage: err instanceof Error ? err.message : 'Unknown error',
        timestamp: new Date().toISOString(),
        currentUrl: window.location.href,
      });
      error = 'Invalid credentials. Please try again.';
    } finally {
      logger.info('[LOGIN_DEBUG] Login process finished', {
        isSubmitting: false,
        timestamp: new Date().toISOString(),
      });
      isSubmitting = false;
    }
  }

  // SECURITY: Validate OAuth endpoints before redirect
  function handleOAuthLogin(provider: 'google' | 'github') {
    // SECURITY: Validate OAuth endpoints are properly configured
    const oauthEndpoints = {
      google: '/api/v1/auth/google',
      github: '/api/v1/auth/github',
    };

    // eslint-disable-next-line security/detect-object-injection
    const endpoint = oauthEndpoints[provider];

    // SECURITY: Basic endpoint validation
    if (!endpoint || !endpoint.startsWith('/api/v1/auth/')) {
      error = t('auth.errors.configurationError');
      return;
    }

    if (provider === 'google') {
      googleLoading = true;
    } else {
      githubLoading = true;
    }

    // Redirect to OAuth provider
    window.location.href = endpoint;
  }

  function handleSubmit(event: Event) {
    event.preventDefault();
    handlePasswordLogin();
  }

  // SECURITY: Secure state cleanup
  $effect(() => {
    if (!isOpen) {
      // Clear all sensitive state when modal closes
      password = '';
      error = '';
      isSubmitting = false;
      googleLoading = false;
      githubLoading = false;
      // Don't reset rate limiting state when modal closes
    }
  });
</script>

{#if isOpen}
  <div class="modal modal-open">
    <div class="modal-box sm:p-6 sm:pb-10 p-3 overflow-y-auto" role="dialog">
      <form onsubmit={handleSubmit}>
        <input type="hidden" name="redirect" value={redirectUrl} />

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
            <h3 class="text-xl font-black py-2 px-6">Login to BirdNET-Go</h3>
            {#if authConfig.basicEnabled}
              <div class="form-control p-6 mx-2 xs:ml-0 xs:mx-14">
                <label class="label" for="password">Password</label>
                <input
                  type="password"
                  id="loginPassword"
                  bind:value={password}
                  class="input input-bordered"
                  required
                  disabled={isSubmitting || isRateLimited}
                  autocomplete="current-password"
                  aria-required="true"
                  aria-labelledby="passwordLabel"
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
                {#if isRateLimited}
                  <div class="text-orange-600 relative mt-2" role="alert">
                    Too many login attempts. Please wait {RATE_LIMIT_MINUTES} minutes before trying again.
                  </div>
                {/if}
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
              disabled={isSubmitting}
              aria-label="Cancel login"
            >
              Cancel
            </button>
            <button
              type="submit"
              class="btn btn-primary grow pr-10"
              disabled={isSubmitting || isRateLimited || !password}
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
                disabled={googleLoading || isRateLimited}
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
                disabled={githubLoading || isRateLimited}
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
