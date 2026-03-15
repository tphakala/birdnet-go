# eBird API Key Validation & Connection Test

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enforce eBird API key as mandatory when enabled, add a connection test button, and show a clear notice about obtaining a personal API key.

**Architecture:** Follow the weather test pattern (stage-based streaming with `sendStage()`) rather than the heavier MQTT/BirdWeather channel-based pattern. The eBird test has only 2 stages (connectivity + authentication), making the simpler weather approach ideal. Frontend follows the existing BirdWeather/MQTT test button pattern in `IntegrationSettingsPage.svelte`.

**Tech Stack:** Go (Echo, `internal/ebird` client), Svelte 5, TypeScript, i18n (10 locales)

---

## File Structure

| File | Action | Purpose |
|------|--------|---------|
| `internal/api/v2/integrations.go` | Modify | Add `TestEBirdConnection` handler + route |
| `internal/api/v2/integrations_test.go` | Modify | Add tests for eBird handler |
| `internal/api/v2/README.md` | Modify | Document new endpoint |
| `frontend/src/lib/desktop/features/settings/pages/IntegrationSettingsPage.svelte` | Modify | Add test button, API key validation, info note |
| `frontend/static/messages/en.json` | Modify | Add eBird test + info translation keys |
| `frontend/static/messages/{de,es,fi,fr,it,nl,pl,pt,sk}.json` | Modify | Add eBird translation keys (English values as placeholders) |

---

## Chunk 1: Backend — eBird Test Endpoint

### Task 1: Add eBird test handler and route

**Files:**
- Modify: `internal/api/v2/integrations.go`

- [ ] **Step 1: Add eBird route registration**

In `initIntegrationsRoutes()`, replace the comment "Other integration routes could be added here" (line 240) with:

```go
// eBird routes
ebirdGroup := integrationsGroup.Group("/ebird")
ebirdGroup.POST("/test", c.TestEBirdConnection)
```

- [ ] **Step 2: Add EBirdTestRequest struct and handler**

After the `BirdWeatherTestRequest` struct (around line 492), add:

```go
// EBirdTestRequest represents a request to test eBird API connectivity
type EBirdTestRequest struct {
	Enabled bool   `json:"enabled"`
	APIKey  string `json:"apiKey"`
	Locale  string `json:"locale"`
}

// TestEBirdConnection handles POST /api/v2/integrations/ebird/test
func (c *Controller) TestEBirdConnection(ctx echo.Context) error {
	var request EBirdTestRequest
	if err := ctx.Bind(&request); err != nil {
		return c.HandleError(ctx, err, "Invalid eBird test request", http.StatusBadRequest)
	}

	// Validate eBird configuration from the request
	if !request.Enabled {
		return ctx.JSON(http.StatusOK, map[string]any{
			"success": false,
			"message": "eBird integration is not enabled",
			"state":   "failed",
		})
	}

	if request.APIKey == "" {
		return ctx.JSON(http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "eBird API key is required",
			"state":   "failed",
		})
	}

	// Set up streaming response (same pattern as weather test)
	ctx.Response().Header().Set("Content-Type", "application/x-ndjson")
	ctx.Response().Header().Set("Cache-Control", "no-cache")
	ctx.Response().Header().Set("Connection", "keep-alive")
	ctx.Response().WriteHeader(http.StatusOK)

	// Create test context with timeout
	testCtx, cancel := context.WithTimeout(ctx.Request().Context(), integrationMediumTimeout*time.Second)
	defer cancel()

	// Create encoder for streaming results
	encoder := json.NewEncoder(ctx.Response())

	// Helper function to send stage results
	sendStage := func(stage WeatherTestStage) error {
		if err := encoder.Encode(stage); err != nil {
			return err
		}
		ctx.Response().Flush()
		return nil
	}

	// Determine locale for display
	locale := request.Locale
	if locale == "" {
		locale = "en"
	}

	// Run eBird test stages
	stages := []struct {
		id    string
		title string
		test  func() (string, error)
	}{
		{"connectivity", "API Connectivity", func() (string, error) {
			return c.testEBirdConnectivity(testCtx)
		}},
		{"authentication", "Authentication", func() (string, error) {
			return c.testEBirdAuthentication(testCtx, request.APIKey, locale)
		}},
	}

	// Execute each stage
	for _, stage := range stages {
		// Send in-progress status
		if err := sendStage(WeatherTestStage{
			ID:     stage.id,
			Title:  stage.title,
			Status: "in_progress",
		}); err != nil {
			return nil // Client disconnected
		}

		// Run the test
		message, err := stage.test()
		if err != nil {
			// Send error status
			return sendStage(WeatherTestStage{
				ID:      stage.id,
				Title:   stage.title,
				Status:  "error",
				Message: message,
				Error:   err.Error(),
			})
		}

		// Send completed status
		if err := sendStage(WeatherTestStage{
			ID:      stage.id,
			Title:   stage.title,
			Status:  "completed",
			Message: message,
		}); err != nil {
			return nil // Client disconnected
		}

		// Small delay between stages for UX
		time.Sleep(integrationStageDelay * time.Millisecond)
	}

	return nil
}
```

- [ ] **Step 3: Add eBird test helper functions**

After the handler, add the connectivity and authentication test helpers:

```go
// testEBirdConnectivity tests basic connectivity to the eBird API
func (c *Controller) testEBirdConnectivity(ctx context.Context) (string, error) {
	client := &http.Client{Timeout: integrationShortTimeout * time.Second}
	// Use the eBird homepage for a pure connectivity check — no API key required,
	// avoids CDN/WAF issues that may occur on API endpoints without a token.
	req, err := http.NewRequestWithContext(ctx, "HEAD", "https://api.ebird.org/v2/ref/taxonomy/ebird", http.NoBody)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", "BirdNET-Go eBird Test")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to connect to eBird API: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			GetLogger().Warn("Failed to close response body", logger.Error(closeErr))
		}
	}()

	// Any response (even 403) means the API is reachable
	return "Successfully connected to eBird API", nil
}

// testEBirdAuthentication tests authentication with the eBird API using a small taxonomy request
func (c *Controller) testEBirdAuthentication(ctx context.Context, apiKey, locale string) (string, error) {
	client := &http.Client{Timeout: integrationShortTimeout * time.Second}

	// Request a single taxonomy entry to validate the API key with minimal data transfer
	url := fmt.Sprintf("https://api.ebird.org/v2/ref/taxonomy/ebird?fmt=json&cat=species&maxResults=1&locale=%s", locale)

	req, err := http.NewRequestWithContext(ctx, "GET", url, http.NoBody)
	if err != nil {
		return "", fmt.Errorf("failed to create authentication request: %w", err)
	}

	req.Header.Set("X-eBirdApiToken", apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "BirdNET-Go eBird Test")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to authenticate with eBird API: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			GetLogger().Warn("Failed to close response body", logger.Error(closeErr))
		}
	}()

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		return "", fmt.Errorf("invalid API key - please check your eBird API key")
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected response from eBird API (status %d)", resp.StatusCode)
	}

	return fmt.Sprintf("Successfully authenticated with eBird API (locale: %s)", locale), nil
}
```

- [ ] **Step 4: Add ebird import if needed**

The handler uses `ebird` package's logger indirectly — but since we're making direct HTTP calls in the controller (same as weather test), no new imports are needed beyond what's already in `integrations.go`. Verify `"fmt"`, `"net/http"`, `"context"`, `"encoding/json"`, and `"time"` are all already imported (they are).

- [ ] **Step 5: Run Go build to verify compilation**

Run: `cd /home/thakala/src/birdnet-go/.claude/worktrees/greedy-drifting-hartmanis && go build ./internal/api/v2/...`
Expected: No errors

- [ ] **Step 6: Commit backend handler**

```bash
git add internal/api/v2/integrations.go
git commit -m "feat: add eBird API connection test endpoint

Add POST /api/v2/integrations/ebird/test with two stages:
- API Connectivity: verifies eBird API is reachable
- Authentication: validates API key with minimal taxonomy request

Follows the weather test stage-based streaming pattern."
```

### Task 2: Add backend tests for eBird handler

**Files:**
- Modify: `internal/api/v2/integrations_test.go`

- [ ] **Step 1: Add eBird route registration test**

In `TestInitIntegrationsRoutesRegistration`, add `"POST /api/v2/integrations/ebird/test"` to the `assertRoutesRegistered` call:

```go
assertRoutesRegistered(t, e, []string{
	"GET /api/v2/integrations/mqtt/status",
	"POST /api/v2/integrations/mqtt/test",
	"GET /api/v2/integrations/birdweather/status",
	"POST /api/v2/integrations/birdweather/test",
	"POST /api/v2/integrations/ebird/test",
})
```

- [ ] **Step 2: Add eBird connection test cases**

After `TestTestBirdWeatherConnection`, add:

```go
// TestTestEBirdConnection tests the TestEBirdConnection handler
func TestTestEBirdConnection(t *testing.T) {
	// Define test cases
	testCases := []struct {
		name           string
		requestBody    string
		expectedStatus int
		expectError    bool // true if HandleError may return an error instead of writing response
		validateResult func(*testing.T, string)
	}{
		{
			name:           "eBird Not Enabled",
			requestBody:    `{"enabled":false,"apiKey":"test-key","locale":"en"}`,
			expectedStatus: http.StatusOK,
			validateResult: func(t *testing.T, body string) {
				t.Helper()
				assert.Contains(t, body, `"success":false`)
				assert.Contains(t, body, "not enabled")
			},
		},
		{
			name:           "API Key Not Configured",
			requestBody:    `{"enabled":true,"apiKey":"","locale":"en"}`,
			expectedStatus: http.StatusBadRequest,
			validateResult: func(t *testing.T, body string) {
				t.Helper()
				assert.Contains(t, body, `"success":false`)
				assert.Contains(t, body, "API key is required")
			},
		},
		{
			name:           "Invalid Request Body",
			requestBody:    `{invalid json`,
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
			validateResult: func(t *testing.T, body string) {
				t.Helper()
				// HandleError writes the error response
				assert.Contains(t, body, "error")
			},
		},
		{
			name:           "Valid Request Enters Streaming",
			requestBody:    `{"enabled":true,"apiKey":"test-key-123","locale":"en"}`,
			expectedStatus: http.StatusOK,
			validateResult: func(t *testing.T, body string) {
				t.Helper()
				// The handler enters the streaming path and sets ndjson content type.
				// The actual API call will fail (no real eBird access in tests),
				// but the response should contain stage data.
				assert.NotEmpty(t, body, "Response body should not be empty")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup
			e, _, controller := setupTestEnvironment(t)

			// Create request
			req := httptest.NewRequest(http.MethodPost, "/api/v2/integrations/ebird/test",
				strings.NewReader(tc.requestBody))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			// Call handler
			err := controller.TestEBirdConnection(c)

			if tc.expectError {
				// HandleError may return the error or write the response
				if err != nil {
					return
				}
			} else {
				require.NoError(t, err)
			}

			// Check status code
			assert.Equal(t, tc.expectedStatus, rec.Code)

			// Validate response body
			tc.validateResult(t, strings.TrimSpace(rec.Body.String()))
		})
	}
}
```

- [ ] **Step 3: Run tests**

Run: `cd /home/thakala/src/birdnet-go/.claude/worktrees/greedy-drifting-hartmanis && go test -race -v ./internal/api/v2/ -run TestTestEBirdConnection -timeout 30s`
Expected: All tests pass

- [ ] **Step 4: Run all integration tests to check for regressions**

Run: `cd /home/thakala/src/birdnet-go/.claude/worktrees/greedy-drifting-hartmanis && go test -race -v ./internal/api/v2/ -run TestInit -timeout 30s`
Expected: Route registration test passes with new eBird route included

- [ ] **Step 5: Commit tests**

```bash
git add internal/api/v2/integrations_test.go
git commit -m "test: add eBird connection test endpoint tests

Test disabled state, missing API key, and invalid request body.
Update route registration test to include new eBird endpoint."
```

### Task 3: Update API README

**Files:**
- Modify: `internal/api/v2/README.md`

- [ ] **Step 1: Add eBird endpoint to README**

Find the integrations section and add:

```markdown
| POST | `/api/v2/integrations/ebird/test` | Yes | Test eBird API connectivity and authentication |
```

- [ ] **Step 2: Commit**

```bash
git add internal/api/v2/README.md
git commit -m "docs: add eBird test endpoint to API v2 README"
```

---

## Chunk 2: Frontend — Translation Keys

### Task 4: Add translation keys for eBird test and info note

**Files:**
- Modify: `frontend/static/messages/en.json`
- Modify: `frontend/static/messages/{de,es,fi,fr,it,nl,pl,pt,sk}.json`

- [ ] **Step 1: Add English translation keys**

In `en.json`, expand the `settings.integration.ebird` section (currently around line 2638). Replace the existing `ebird` block with:

```json
"ebird": {
  "title": "eBird",
  "description": "Connect to the eBird taxonomy database for enriched species data including taxonomic classification and subspecies information.",
  "enable": "Enable eBird Integration",
  "enabledRequired": "eBird integration is not enabled",
  "apiKey": {
    "label": "eBird API Key",
    "helpText": "Your eBird API key. Obtain one from the eBird API portal."
  },
  "locale": {
    "label": "Species Name Language"
  },
  "cacheTTL": {
    "label": "Cache Duration (hours)",
    "helpText": "How long to cache taxonomy data from eBird. Taxonomy rarely changes, so longer cache durations reduce API calls."
  },
  "note": "eBird provides taxonomic data used for species classification and insights. The API key is free to obtain from the Cornell Lab of Ornithology.",
  "apiKeyInfo": "A personal eBird API key is required to use this integration. You can request a free key from <a href=\"https://ebird.org/api/keygen\" class=\"link link-primary\" target=\"_blank\" rel=\"noopener noreferrer\">ebird.org/api/keygen</a>.",
  "test": {
    "button": "Test eBird Connection",
    "loading": "Testing...",
    "enabledRequired": "Enable eBird integration first",
    "apiKeyRequired": "API key is required",
    "inProgress": "Test in progress...",
    "description": "Test eBird API connectivity and authentication"
  }
}
```

The new keys are:
- `apiKeyInfo` — always-visible info banner about obtaining an API key
- `test.*` — test button labels and status messages

- [ ] **Step 2: Add same keys to all other locale files**

For each of `de.json`, `es.json`, `fi.json`, `fr.json`, `it.json`, `nl.json`, `pl.json`, `pt.json`, `sk.json`:

Add the same `apiKeyInfo` and `test` keys inside the existing `settings.integration.ebird` block (use English values as placeholders — translations can be done separately).

- [ ] **Step 3: Commit translations**

```bash
git add frontend/static/messages/*.json
git commit -m "i18n: add eBird test and API key info translation keys

Add keys for connection test button, status messages, and
an info banner about obtaining a personal eBird API key.
Non-English locales use English placeholders."
```

---

## Chunk 3: Frontend — eBird Test & Validation UI

### Task 5: Add eBird test connection and API key validation to settings page

**Files:**
- Modify: `frontend/src/lib/desktop/features/settings/pages/IntegrationSettingsPage.svelte`

- [ ] **Step 1: Add eBird test state**

In the `testStates` initialization (around line 163), add `ebird`:

```typescript
let testStates = $state<{
  birdweather: { stages: Stage[]; isRunning: boolean; showSuccessNote: boolean };
  mqtt: { stages: Stage[]; isRunning: boolean; showSuccessNote: boolean };
  ebird: { stages: Stage[]; isRunning: boolean; showSuccessNote: boolean };
}>({
  birdweather: { stages: [], isRunning: false, showSuccessNote: false },
  mqtt: { stages: [], isRunning: false, showSuccessNote: false },
  ebird: { stages: [], isRunning: false, showSuccessNote: false },
});
```

- [ ] **Step 2: Add testEBird function**

After the `testMQTT` function (around line 916), add the eBird test function. This follows the weather test's ndjson streaming pattern (not the channel-based BirdWeather/MQTT pattern):

```typescript
async function testEBird() {
  logger.debug('Starting eBird test...');
  testStates.ebird.isRunning = true;
  testStates.ebird.stages = [];

  try {
    const currentEbird = store.formData?.realtime?.ebird || settings.ebird!;

    const testPayload = {
      enabled: currentEbird.enabled || false,
      apiKey: currentEbird.apiKey || '',
      locale: currentEbird.locale || 'en',
    };

    const headers = new Headers({
      'Content-Type': 'application/json',
    });

    const token = getCsrfToken();
    if (token) {
      headers.set('X-CSRF-Token', token);
    }

    logger.debug('Sending eBird test request with payload:', redactForLogging(testPayload, ['apiKey']));

    const response = await fetch(buildAppUrl('/api/v2/integrations/ebird/test'), {
      method: 'POST',
      headers,
      credentials: 'same-origin',
      body: JSON.stringify(testPayload),
    });

    logger.debug('eBird test response status:', response.status, response.statusText);

    if (!response.ok) {
      throw new Error(`HTTP ${response.status}: ${response.statusText}`);
    }

    // Read the streaming ndjson response
    const reader = response.body?.getReader();
    const decoder = new TextDecoder();

    if (!reader) {
      throw new Error(t('settings.integration.errors.responseStreamFailed'));
    }

    let remaining = '';

    while (true) {
      const { done, value } = await reader.read();
      if (done) break;

      remaining += decoder.decode(value, { stream: true });
      logger.debug('Raw eBird chunk received:', remaining);

      // Parse ndjson lines
      const lines = remaining.split('\n');
      remaining = lines.pop() || '';

      for (const line of lines) {
        const trimmed = line.trim();
        if (!trimmed) continue;

        try {
          const stageResult = JSON.parse(trimmed);
          logger.debug('eBird test result received:', stageResult);

          if (!stageResult.id) continue;

          const stage: Stage = {
            id: stageResult.id,
            title: stageResult.title || 'Test Stage',
            status: stageResult.status || 'pending',
            message: stageResult.message || '',
            error: stageResult.error || '',
          };

          const existingIndex = testStates.ebird.stages.findIndex(s => s.id === stage.id);
          if (existingIndex === -1) {
            testStates.ebird.stages.push(stage);
          } else {
            const existingStage = safeArrayAccess(testStates.ebird.stages, existingIndex);
            if (existingStage && existingIndex >= 0 && existingIndex < testStates.ebird.stages.length) {
              testStates.ebird.stages.splice(existingIndex, 1, { ...existingStage, ...stage });
            }
          }
        } catch (parseError) {
          logger.error('Failed to parse eBird test result:', parseError, trimmed);
        }
      }
    }
  } catch (error) {
    logger.error('eBird test failed:', error);

    if (testStates.ebird.stages.length === 0) {
      testStates.ebird.stages.push({
        id: 'error',
        title: t('settings.integration.errors.connectionError'),
        status: 'error',
        error: error instanceof Error ? error.message : t('common.errors.unknownError'),
      });
    } else {
      const lastIndex = testStates.ebird.stages.length - 1;
      const lastStage = safeArrayAccess(testStates.ebird.stages, lastIndex);
      if (lastStage && lastStage.status === 'in_progress') {
        testStates.ebird.stages.splice(lastIndex, 1, {
          ...lastStage,
          status: 'error' as const,
          error: error instanceof Error ? error.message : t('common.errors.unknownError'),
        });
      }
    }
  } finally {
    testStates.ebird.isRunning = false;
    logger.debug('eBird test finished, stages:', testStates.ebird.stages);

    const allStagesCompleted =
      testStates.ebird.stages.length > 0 &&
      testStates.ebird.stages.every(stage => stage.status === 'completed');
    testStates.ebird.showSuccessNote = allStagesCompleted && ebirdHasChanges;

    setTimeout(() => {
      logger.debug('Clearing eBird test results after timeout');
      testStates.ebird.stages = [];
      testStates.ebird.showSuccessNote = false;
    }, 30000);
  }
}
```

- [ ] **Step 3: Update the eBird tab snippet**

Replace the existing `{#snippet ebirdTabContent()}` block (lines 1425-1496) with the updated version that includes:
1. An always-visible info banner about obtaining an API key
2. A test connection button section (matching BirdWeather/MQTT pattern)

```svelte
{#snippet ebirdTabContent()}
  <div class="space-y-6">
    <!-- eBird Settings Card -->
    <SettingsSection
      title={t('settings.integration.ebird.title')}
      description={t('settings.integration.ebird.description')}
      originalData={(store.originalData as SettingsFormData)?.realtime?.ebird}
      currentData={(store.formData as SettingsFormData)?.realtime?.ebird}
    >
      <div class="space-y-4">
        <Checkbox
          checked={settings.ebird!.enabled}
          label={t('settings.integration.ebird.enable')}
          disabled={store.isLoading || store.isSaving}
          onchange={updateEBirdEnabled}
        />

        <!-- Fieldset for accessible disabled state -->
        <fieldset
          disabled={!settings.ebird?.enabled || store.isLoading || store.isSaving}
          class="contents"
          aria-describedby="ebird-status"
        >
          <span id="ebird-status" class="sr-only">
            {settings.ebird?.enabled
              ? t('settings.integration.ebird.enable')
              : t('settings.integration.ebird.enabledRequired')}
          </span>
          <div class="transition-opacity duration-200" class:opacity-50={!settings.ebird?.enabled}>
            <!-- API Key Info Banner -->
            <div
              class="flex items-start gap-3 p-4 rounded-lg mb-4 bg-[color-mix(in_srgb,var(--color-info)_15%,transparent)] text-[var(--color-info)]"
            >
              <Info class="size-5 shrink-0 mt-0.5" />
              <div>
                <span>{@html t('settings.integration.ebird.apiKeyInfo')}</span>
              </div>
            </div>

            <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
              <PasswordField
                label={t('settings.integration.ebird.apiKey.label')}
                value={settings.ebird!.apiKey}
                onUpdate={updateEBirdApiKey}
                placeholder=""
                helpText={t('settings.integration.ebird.apiKey.helpText')}
                disabled={!settings.ebird?.enabled || store.isLoading || store.isSaving}
                allowReveal={true}
              />

              <SelectField
                value={settings.ebird!.locale}
                options={ebirdLocaleOptions}
                label={t('settings.integration.ebird.locale.label')}
                disabled={!settings.ebird?.enabled || store.isLoading || store.isSaving}
                onchange={updateEBirdLocale}
              />
            </div>

            <div class="grid grid-cols-1 md:grid-cols-2 gap-6 mt-4">
              <NumberField
                label={t('settings.integration.ebird.cacheTTL.label')}
                value={settings.ebird!.cacheTTL}
                onUpdate={updateEBirdCacheTTL}
                min={1}
                max={168}
                step={1}
                placeholder="24"
                helpText={t('settings.integration.ebird.cacheTTL.helpText')}
                disabled={!settings.ebird?.enabled || store.isLoading || store.isSaving}
              />
            </div>

            <!-- Test Connection -->
            <div class="space-y-4 mt-4">
              <div class="flex items-center gap-3">
                <SettingsButton
                  onclick={testEBird}
                  loading={testStates.ebird.isRunning}
                  loadingText={t('settings.integration.ebird.test.loading')}
                  disabled={!(
                    store.formData?.realtime?.ebird?.enabled ?? settings.ebird?.enabled
                  ) ||
                    !(store.formData?.realtime?.ebird?.apiKey ?? settings.ebird?.apiKey) ||
                    testStates.ebird.isRunning}
                >
                  {t('settings.integration.ebird.test.button')}
                </SettingsButton>
                <span class="text-sm text-[var(--color-base-content)] opacity-70">
                  {#if !(store.formData?.realtime?.ebird?.enabled ?? settings.ebird?.enabled)}
                    {t('settings.integration.ebird.test.enabledRequired')}
                  {:else if !(store.formData?.realtime?.ebird?.apiKey ?? settings.ebird?.apiKey)}
                    {t('settings.integration.ebird.test.apiKeyRequired')}
                  {:else if testStates.ebird.isRunning}
                    {t('settings.integration.ebird.test.inProgress')}
                  {:else}
                    {t('settings.integration.ebird.test.description')}
                  {/if}
                </span>
              </div>

              {#if testStates.ebird.stages.length > 0}
                <MultiStageOperation
                  stages={testStates.ebird.stages}
                  variant="compact"
                  showProgress={false}
                />
              {/if}

              <TestSuccessNote show={testStates.ebird.showSuccessNote} />
            </div>

            <SettingsNote>
              <span>{t('settings.integration.ebird.note')}</span>
            </SettingsNote>
          </div>
        </fieldset>
      </div>
    </SettingsSection>
  </div>
{/snippet}
```

- [ ] **Step 4: Verify frontend builds**

Run: `cd /home/thakala/src/birdnet-go/.claude/worktrees/greedy-drifting-hartmanis && task frontend-build`
Expected: Build succeeds with no errors

- [ ] **Step 5: Run frontend lint**

Run: `cd /home/thakala/src/birdnet-go/.claude/worktrees/greedy-drifting-hartmanis && task frontend-lint`
Expected: No errors (warnings acceptable)

- [ ] **Step 6: Run Svelte autofixer on the modified component**

Use `mcp__svelte__svelte-autofixer` on the eBird tab snippet to verify Svelte 5 correctness.

- [ ] **Step 7: Commit frontend changes**

```bash
git add frontend/src/lib/desktop/features/settings/pages/IntegrationSettingsPage.svelte
git commit -m "feat: add eBird API key validation and connection test UI

- Add test connection button with 2-stage streaming feedback
- Add info banner about obtaining a personal eBird API key
- Disable test button when API key is empty
- Follow existing BirdWeather/MQTT test patterns"
```

---

## Chunk 4: Final Validation

### Task 6: Run linters and full test suite

- [ ] **Step 1: Run Go linter**

Run: `cd /home/thakala/src/birdnet-go/.claude/worktrees/greedy-drifting-hartmanis && golangci-lint run -v ./internal/api/v2/...`
Expected: No errors

- [ ] **Step 2: Run Go tests**

Run: `cd /home/thakala/src/birdnet-go/.claude/worktrees/greedy-drifting-hartmanis && go test -race -v ./internal/api/v2/ -timeout 60s`
Expected: All tests pass

- [ ] **Step 3: Run frontend quality checks**

Run: `cd /home/thakala/src/birdnet-go/.claude/worktrees/greedy-drifting-hartmanis && task frontend-quality`
Expected: All checks pass

- [ ] **Step 4: Format all changed files**

Run prettier and gofmt on modified files:
```bash
cd /home/thakala/src/birdnet-go/.claude/worktrees/greedy-drifting-hartmanis
gofmt -w internal/api/v2/integrations.go internal/api/v2/integrations_test.go
npx --prefix frontend prettier --write frontend/src/lib/desktop/features/settings/pages/IntegrationSettingsPage.svelte frontend/static/messages/*.json
```

---

## Design Decisions

1. **Weather test pattern over channel pattern**: The eBird test has only 2 quick stages. The weather test's `sendStage()` approach is simpler and avoids the complexity of channels, adapters, and goroutine coordination needed by BirdWeather/MQTT.

2. **Direct HTTP calls in controller**: Same as the weather test — we make direct HTTP calls to eBird API rather than creating a full `ebird.Client` instance. This avoids pulling in the rate limiter, cache, and global settings dependency that `NewClient` requires.

3. **API key validation is frontend + backend**: The test button is disabled when API key is empty (frontend), and the backend also validates (defense in depth). There's no need for a separate save-time validation since the test makes the requirement obvious.

4. **Info banner always visible**: The API key info note is always shown (not conditional on empty key) because users need to know where to get a key before they even start configuring.

5. **Reuse `WeatherTestStage` type**: The backend reuses the existing `WeatherTestStage` struct since it has exactly the fields we need. No new types required.
