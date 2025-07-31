<script lang="ts">
  import NumberField from '$lib/desktop/components/forms/NumberField.svelte';
  import Checkbox from '$lib/desktop/components/forms/Checkbox.svelte';
  import SelectField from '$lib/desktop/components/forms/SelectField.svelte';
  import TextInput from '$lib/desktop/components/forms/TextInput.svelte';
  import PasswordField from '$lib/desktop/components/forms/PasswordField.svelte';
  import SettingsSection from '$lib/desktop/features/settings/components/SettingsSection.svelte';
  import MultiStageOperation from '$lib/desktop/components/ui/MultiStageOperation.svelte';
  import SettingsButton from '$lib/desktop/features/settings/components/SettingsButton.svelte';
  import SettingsNote from '$lib/desktop/features/settings/components/SettingsNote.svelte';
  import TestSuccessNote from '$lib/desktop/components/ui/TestSuccessNote.svelte';
  import { alertIconsSvg } from '$lib/utils/icons'; // Centralized icons - see icons.ts
  import {
    settingsStore,
    settingsActions,
    integrationSettings,
    realtimeSettings,
    type SettingsFormData,
    type MQTTSettings,
    type WeatherSettings,
  } from '$lib/stores/settings';
  import { hasSettingsChanged } from '$lib/utils/settingsChanges';
  import type { Stage } from '$lib/desktop/components/ui/MultiStageOperation.types';
  import { getCsrfToken } from '$lib/utils/api.js';
  import { t } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';

  const logger = loggers.settings;

  let settings = $derived(
    $integrationSettings || {
      birdweather: {
        enabled: false,
        id: '',
        latitude: 0,
        longitude: 0,
        locationAccuracy: 1000,
        threshold: 0.7,
        debug: false,
      },
      mqtt: {
        enabled: false,
        broker: '',
        port: 1883,
        username: '',
        password: '',
        topic: 'birdnet',
        retain: false,
        tls: {
          enabled: false,
          skipVerify: false,
        },
      },
      observability: {
        prometheus: {
          enabled: false,
          port: 9090,
          path: '/metrics',
        },
      },
      weather: {
        provider: 'yrno' as 'none' | 'yrno' | 'openweather',
        pollInterval: 60,
        debug: false,
        openWeather: {
          enabled: false,
          apiKey: '',
          endpoint: 'https://api.openweathermap.org/data/2.5/weather',
          units: 'metric',
          language: 'en',
        },
      },
    }
  );

  let store = $derived($settingsStore);

  // Track changes for each section separately using proper typing
  let birdweatherHasChanges = $derived(
    hasSettingsChanged(
      (store.originalData as SettingsFormData)?.realtime?.birdweather,
      (store.formData as SettingsFormData)?.realtime?.birdweather
    )
  );

  let mqttHasChanges = $derived(
    hasSettingsChanged(
      (store.originalData as SettingsFormData)?.realtime?.mqtt,
      (store.formData as SettingsFormData)?.realtime?.mqtt
    )
  );

  let observabilityHasChanges = $derived(
    hasSettingsChanged(
      // Observability is actually derived from telemetry in the store
      (store.originalData as SettingsFormData)?.realtime?.telemetry,
      (store.formData as SettingsFormData)?.realtime?.telemetry
    )
  );

  let weatherHasChanges = $derived(
    hasSettingsChanged(
      (store.originalData as SettingsFormData)?.realtime?.weather,
      (store.formData as SettingsFormData)?.realtime?.weather
    )
  );

  // Test states for multi-stage operations
  let testStates = $state<{
    birdweather: { stages: Stage[]; isRunning: boolean; showSuccessNote: boolean };
    mqtt: { stages: Stage[]; isRunning: boolean; showSuccessNote: boolean };
    weather: { stages: Stage[]; isRunning: boolean; showSuccessNote: boolean };
  }>({
    birdweather: { stages: [], isRunning: false, showSuccessNote: false },
    mqtt: { stages: [], isRunning: false, showSuccessNote: false },
    weather: { stages: [], isRunning: false, showSuccessNote: false },
  });

  // FFmpeg availability check
  let ffmpegAvailable = $state(true);

  // BirdWeather update handlers
  function updateBirdWeatherEnabled(enabled: boolean) {
    settingsActions.updateSection('realtime', {
      birdweather: { ...settings.birdweather!, enabled },
    });
  }

  function updateBirdWeatherId(id: string) {
    settingsActions.updateSection('realtime', {
      birdweather: { ...settings.birdweather!, id },
    });
  }

  function updateBirdWeatherThreshold(threshold: number) {
    settingsActions.updateSection('realtime', {
      birdweather: { ...settings.birdweather!, threshold },
    });
  }

  // MQTT update handlers
  function updateMQTTEnabled(enabled: boolean) {
    settingsActions.updateSection('realtime', {
      mqtt: { ...settings.mqtt!, enabled },
    });
  }

  function updateMQTTBroker(broker: string) {
    settingsActions.updateSection('realtime', {
      mqtt: { ...settings.mqtt!, broker },
    });
  }

  function updateMQTTTopic(topic: string) {
    settingsActions.updateSection('realtime', {
      mqtt: { ...settings.mqtt!, topic },
    });
  }

  function updateMQTTUsername(username: string) {
    settingsActions.updateSection('realtime', {
      mqtt: { ...settings.mqtt!, username },
    });
  }

  function updateMQTTPassword(password: string) {
    settingsActions.updateSection('realtime', {
      mqtt: { ...settings.mqtt!, password },
    });
  }

  function updateMQTTTLSEnabled(enabled: boolean) {
    settingsActions.updateSection('realtime', {
      mqtt: { ...settings.mqtt!, tls: { ...settings.mqtt!.tls, enabled } },
    });
  }

  function updateMQTTTLSSkipVerify(skipVerify: boolean) {
    settingsActions.updateSection('realtime', {
      mqtt: { ...settings.mqtt!, tls: { ...settings.mqtt!.tls, skipVerify } },
    });
  }

  function updateMQTTRetain(retain: boolean) {
    settingsActions.updateSection('realtime', {
      mqtt: { ...(settings.mqtt as MQTTSettings), retain },
    });
  }

  // Observability update handlers
  function updateObservabilityEnabled(enabled: boolean) {
    settingsActions.updateSection('realtime', {
      telemetry: {
        enabled,
        listen: $realtimeSettings?.telemetry?.listen || '0.0.0.0:8090',
      },
    });
  }

  function updateObservabilityListen(listen: string) {
    settingsActions.updateSection('realtime', {
      telemetry: {
        enabled: $realtimeSettings?.telemetry?.enabled || false,
        listen,
      },
    });
  }

  // Weather update handlers
  function updateWeatherProvider(provider: string) {
    settingsActions.updateSection('realtime', {
      weather: { ...settings.weather!, provider: provider as 'none' | 'yrno' | 'openweather' },
    });
  }

  function updateWeatherApiKey(apiKey: string) {
    settingsActions.updateSection('realtime', {
      weather: { ...settings.weather!, openWeather: { ...settings.weather!.openWeather, apiKey } },
    });
  }

  function updateWeatherUnits(units: string) {
    settingsActions.updateSection('realtime', {
      weather: { ...settings.weather!, openWeather: { ...settings.weather!.openWeather, units } },
    });
  }

  // Test functions with multi-stage operations
  async function testBirdWeather() {
    logger.debug('Starting BirdWeather test...');
    testStates.birdweather.isRunning = true;
    testStates.birdweather.stages = [];

    try {
      // Get current form values (unsaved changes) instead of saved settings
      const currentBirdweather = store.formData?.realtime?.birdweather || settings.birdweather!;
      logger.debug('BirdWeather test config:', currentBirdweather);

      // Prepare test payload
      const testPayload = {
        enabled: currentBirdweather.enabled || false,
        id: currentBirdweather.id || '',
        threshold: currentBirdweather.threshold || 0.7,
        locationAccuracy: currentBirdweather.locationAccuracy || 1000,
        debug: currentBirdweather.debug || false,
      };

      // Make request to the real API with CSRF token
      const headers = new Headers({
        'Content-Type': 'application/json',
      });

      const csrfToken = getCsrfToken();
      if (csrfToken) {
        headers.set('X-CSRF-Token', csrfToken);
      }

      logger.debug('Sending BirdWeather test request with payload:', testPayload);

      const response = await fetch('/api/v2/integrations/birdweather/test', {
        method: 'POST',
        headers,
        credentials: 'same-origin',
        body: JSON.stringify(testPayload),
      });

      logger.debug('BirdWeather test response status:', response.status, response.statusText);

      if (!response.ok) {
        throw new Error(`HTTP ${response.status}: ${response.statusText}`);
      }

      // Read the streaming response
      const reader = response.body?.getReader();
      const decoder = new TextDecoder();

      if (!reader) {
        throw new Error('Failed to read response stream');
      }

      while (true) {
        const { done, value } = await reader.read();
        if (done) break;

        // Parse each chunk as JSON
        const chunk = decoder.decode(value);
        logger.debug('Raw BirdWeather chunk received:', chunk);

        // Split by both newlines and by '}{'  pattern to handle concatenated JSON objects
        const jsonObjects = [];
        let remaining = chunk;

        while (remaining.trim()) {
          try {
            // Find the end of the first complete JSON object
            let braceCount = 0;
            let jsonEnd = -1;

            for (let i = 0; i < remaining.length; i++) {
              if (remaining[i] === '{') braceCount++;
              if (remaining[i] === '}') braceCount--;
              if (braceCount === 0) {
                jsonEnd = i + 1;
                break;
              }
            }

            if (jsonEnd === -1) break; // No complete JSON object found

            const jsonStr = remaining.substring(0, jsonEnd).trim();
            if (jsonStr) {
              jsonObjects.push(jsonStr);
            }

            remaining = remaining.substring(jsonEnd).trim();
          } catch (e) {
            logger.error('Error splitting JSON objects:', e);
            break;
          }
        }

        for (const jsonStr of jsonObjects) {
          try {
            const stageResult = JSON.parse(jsonStr);
            logger.debug('BirdWeather test result received:', stageResult);

            // Handle initial failure responses that don't have a stage
            if (!stageResult.stage) {
              // If this is a failed result without stages, show it as an error
              if (stageResult.success === false && stageResult.message) {
                logger.debug('Handling initial error response:', stageResult);
                testStates.birdweather.stages.push({
                  id: 'initial-error',
                  title: 'Configuration Check',
                  status: 'error',
                  message: stageResult.message,
                  error: stageResult.message,
                });
              } else {
                logger.debug('Skipping result without stage:', stageResult);
              }
              continue;
            }

            // Convert BirdWeather TestResult to Stage format
            const stageId = stageResult.stage.toLowerCase().replace(/\\s+/g, '');

            // Determine status based on the BirdWeather TestResult structure
            let status: 'pending' | 'in_progress' | 'completed' | 'error' | 'skipped';
            if (stageResult.isProgress) {
              status = 'in_progress';
            } else if (stageResult.success) {
              status = 'completed';
            } else {
              status = 'error';
            }

            const stage = {
              id: stageId,
              title: stageResult.stage || 'Test Stage',
              status,
              message: stageResult.message || '',
              error: stageResult.error || '',
            };

            logger.debug('Adding/updating BirdWeather stage:', stage);

            // Find existing stage or create new one
            let existingIndex = testStates.birdweather.stages.findIndex(s => s.id === stage.id);
            if (existingIndex === -1) {
              // Add new stage
              testStates.birdweather.stages.push(stage);
            } else {
              // Update existing stage
              testStates.birdweather.stages[existingIndex] = {
                ...testStates.birdweather.stages[existingIndex],
                ...stage,
              };
            }

            logger.debug('Current BirdWeather stages:', testStates.birdweather.stages);
          } catch (parseError) {
            logger.error('Failed to parse BirdWeather test result:', parseError, jsonStr);
          }
        }
      }
    } catch (error) {
      logger.error('BirdWeather test failed:', error);

      // Add error stage if no stages exist
      if (testStates.birdweather.stages.length === 0) {
        testStates.birdweather.stages.push({
          id: 'error',
          title: 'Connection Error',
          status: 'error',
          error: error instanceof Error ? error.message : 'Unknown error occurred',
        });
      } else {
        // Mark current stage as failed
        const lastStage = testStates.birdweather.stages[testStates.birdweather.stages.length - 1];
        if (lastStage.status !== 'completed') {
          lastStage.status = 'error';
          lastStage.error = error instanceof Error ? error.message : 'Unknown error occurred';
        }
      }
    } finally {
      testStates.birdweather.isRunning = false;
      logger.debug('BirdWeather test finished, stages:', testStates.birdweather.stages);

      // Check if all stages completed successfully and there are unsaved changes
      const allStagesCompleted =
        testStates.birdweather.stages.length > 0 &&
        testStates.birdweather.stages.every(stage => stage.status === 'completed');
      testStates.birdweather.showSuccessNote = allStagesCompleted && birdweatherHasChanges;

      // Increase timeout to 30 seconds so users can see the results
      setTimeout(() => {
        logger.debug('Clearing BirdWeather test results after timeout');
        testStates.birdweather.stages = [];
        testStates.birdweather.showSuccessNote = false;
      }, 30000);
    }
  }

  async function testMQTT() {
    logger.debug('Starting MQTT test...');
    testStates.mqtt.isRunning = true;
    testStates.mqtt.stages = [];

    try {
      // Get current form values (unsaved changes) instead of saved settings
      const currentMqtt = store.formData?.realtime?.mqtt || settings.mqtt!;
      logger.debug('MQTT test config:', currentMqtt);

      // Prepare test payload matching the MQTT handler's TestConfig structure
      const testPayload = {
        enabled: currentMqtt.enabled || false,
        broker: currentMqtt.broker || '',
        topic: currentMqtt.topic || 'birdnet',
        username: currentMqtt.username || '',
        password: currentMqtt.password || '',
        retain: (currentMqtt as MQTTSettings).retain || false,
        tls: {
          insecureSkipVerify: currentMqtt.tls?.skipVerify || false,
          caCert: '',
          clientCert: '',
          clientKey: '',
        },
      };

      // Make request to the real API with CSRF token
      const headers = new Headers({
        'Content-Type': 'application/json',
      });

      const csrfToken = getCsrfToken();
      if (csrfToken) {
        headers.set('X-CSRF-Token', csrfToken);
      }

      logger.debug('Sending MQTT test request with payload:', testPayload);

      const response = await fetch('/api/v2/integrations/mqtt/test', {
        method: 'POST',
        headers,
        credentials: 'same-origin',
        body: JSON.stringify(testPayload),
      });

      logger.debug('MQTT test response status:', response.status, response.statusText);

      if (!response.ok) {
        throw new Error(`HTTP ${response.status}: ${response.statusText}`);
      }

      // Read the streaming response
      const reader = response.body?.getReader();
      const decoder = new TextDecoder();

      if (!reader) {
        throw new Error('Failed to read response stream');
      }

      while (true) {
        const { done, value } = await reader.read();
        if (done) break;

        // Parse each line as JSON
        const chunk = decoder.decode(value);
        logger.debug('Raw MQTT chunk received:', chunk);

        // Split by both newlines and by '}{'  pattern to handle concatenated JSON objects
        const jsonObjects = [];
        let remaining = chunk;

        while (remaining.trim()) {
          try {
            // Find the end of the first complete JSON object
            let braceCount = 0;
            let jsonEnd = -1;

            for (let i = 0; i < remaining.length; i++) {
              if (remaining[i] === '{') braceCount++;
              if (remaining[i] === '}') braceCount--;
              if (braceCount === 0) {
                jsonEnd = i + 1;
                break;
              }
            }

            if (jsonEnd === -1) break; // No complete JSON object found

            const jsonStr = remaining.substring(0, jsonEnd).trim();
            if (jsonStr) {
              jsonObjects.push(jsonStr);
            }

            remaining = remaining.substring(jsonEnd).trim();
          } catch (e) {
            logger.error('Error splitting JSON objects:', e);
            break;
          }
        }

        for (const jsonStr of jsonObjects) {
          try {
            const stageResult = JSON.parse(jsonStr);
            logger.debug('MQTT test result received:', stageResult);

            // Skip results that don't have a stage (like elapsed time info)
            if (!stageResult.stage) {
              logger.debug('Skipping result without stage:', stageResult);
              continue;
            }

            // Convert MQTT TestResult to Stage format
            const stageId = stageResult.stage.toLowerCase().replace(/\\s+/g, '');

            // Determine status based on the MQTT TestResult structure
            let status: 'pending' | 'in_progress' | 'completed' | 'error' | 'skipped';
            if (stageResult.isProgress) {
              status = 'in_progress';
            } else if (stageResult.success) {
              status = 'completed';
            } else {
              status = 'error';
            }

            const stage = {
              id: stageId,
              title: stageResult.stage || 'Test Stage',
              status,
              message: stageResult.message || '',
              error: stageResult.error || '',
            };

            logger.debug('Adding/updating MQTT stage:', stage);

            // Find existing stage or create new one
            let existingIndex = testStates.mqtt.stages.findIndex(s => s.id === stage.id);
            if (existingIndex === -1) {
              // Add new stage
              testStates.mqtt.stages.push(stage);
            } else {
              // Update existing stage
              testStates.mqtt.stages[existingIndex] = {
                ...testStates.mqtt.stages[existingIndex],
                ...stage,
              };
            }

            logger.debug('Current MQTT stages:', testStates.mqtt.stages);
          } catch (parseError) {
            logger.error('Failed to parse MQTT test result:', parseError, jsonStr);
          }
        }
      }
    } catch (error) {
      logger.error('MQTT test failed:', error);

      // Add error stage if no stages exist
      if (testStates.mqtt.stages.length === 0) {
        testStates.mqtt.stages.push({
          id: 'error',
          title: 'Connection Error',
          status: 'error',
          error: error instanceof Error ? error.message : 'Unknown error occurred',
        });
      } else {
        // Mark current stage as failed
        const lastStage = testStates.mqtt.stages[testStates.mqtt.stages.length - 1];
        if (lastStage.status !== 'completed') {
          lastStage.status = 'error';
          lastStage.error = error instanceof Error ? error.message : 'Unknown error occurred';
        }
      }
    } finally {
      testStates.mqtt.isRunning = false;
      logger.debug('MQTT test finished, stages:', testStates.mqtt.stages);

      // Check if all stages completed successfully and there are unsaved changes
      const allStagesCompleted =
        testStates.mqtt.stages.length > 0 &&
        testStates.mqtt.stages.every(stage => stage.status === 'completed');
      testStates.mqtt.showSuccessNote = allStagesCompleted && mqttHasChanges;

      // Increase timeout to 30 seconds so users can see the results
      setTimeout(() => {
        logger.debug('Clearing MQTT test results after timeout');
        testStates.mqtt.stages = [];
        testStates.mqtt.showSuccessNote = false;
      }, 30000);
    }
  }

  async function testWeather() {
    testStates.weather.isRunning = true;
    testStates.weather.stages = [];

    try {
      // Get current form values (unsaved changes) instead of saved settings
      const currentWeather = store.formData?.realtime?.weather || settings.weather!;

      // Prepare test payload
      const testPayload = {
        provider: currentWeather.provider || 'none',
        pollInterval: currentWeather.pollInterval || 60,
        debug: currentWeather.debug || false,
        openWeather: {
          apiKey: currentWeather.openWeather?.apiKey || '',
          endpoint: currentWeather.openWeather?.endpoint || '',
          units: currentWeather.openWeather?.units || 'metric',
          language: currentWeather.openWeather?.language || 'en',
        },
      };

      // Make request to the real API with CSRF token
      const headers = new Headers({
        'Content-Type': 'application/json',
      });

      const csrfToken = getCsrfToken();
      if (csrfToken) {
        headers.set('X-CSRF-Token', csrfToken);
      }

      const response = await fetch('/api/v2/integrations/weather/test', {
        method: 'POST',
        headers,
        credentials: 'same-origin',
        body: JSON.stringify(testPayload),
      });

      if (!response.ok) {
        throw new Error(`HTTP ${response.status}: ${response.statusText}`);
      }

      // Read the streaming response
      const reader = response.body?.getReader();
      const decoder = new TextDecoder();

      if (!reader) {
        throw new Error('Failed to read response stream');
      }

      while (true) {
        const { done, value } = await reader.read();
        if (done) break;

        // Parse each line as JSON
        const chunk = decoder.decode(value);
        const lines = chunk.split('\n').filter(line => line.trim());

        for (const line of lines) {
          try {
            const stageResult = JSON.parse(line);

            // Find existing stage or create new one
            let existingIndex = testStates.weather.stages.findIndex(s => s.id === stageResult.id);
            if (existingIndex === -1) {
              // Add new stage
              testStates.weather.stages.push({
                id: stageResult.id,
                title: stageResult.title,
                status: stageResult.status,
                message: stageResult.message,
                error: stageResult.error,
              });
            } else {
              // Update existing stage
              testStates.weather.stages[existingIndex] = {
                ...testStates.weather.stages[existingIndex],
                status: stageResult.status,
                message: stageResult.message,
                error: stageResult.error,
              };
            }
          } catch (parseError) {
            logger.error('Failed to parse stage result:', parseError, line);
          }
        }
      }
    } catch (error) {
      logger.error('Weather test failed:', error);

      // Add error stage if no stages exist
      if (testStates.weather.stages.length === 0) {
        testStates.weather.stages.push({
          id: 'error',
          title: 'Connection Error',
          status: 'error',
          error: error instanceof Error ? error.message : 'Unknown error occurred',
        });
      } else {
        // Mark current stage as failed
        const lastStage = testStates.weather.stages[testStates.weather.stages.length - 1];
        if (lastStage.status === 'in_progress') {
          lastStage.status = 'error';
          lastStage.error = error instanceof Error ? error.message : 'Unknown error occurred';
        }
      }
    } finally {
      testStates.weather.isRunning = false;

      // Check if all stages completed successfully and there are unsaved changes
      const allStagesCompleted =
        testStates.weather.stages.length > 0 &&
        testStates.weather.stages.every(stage => stage.status === 'completed');
      testStates.weather.showSuccessNote = allStagesCompleted && weatherHasChanges;

      setTimeout(() => {
        testStates.weather.stages = [];
        testStates.weather.showSuccessNote = false;
      }, 15000);
    }
  }
</script>

{#if store.isLoading}
  <div class="flex items-center justify-center py-12">
    <div class="loading loading-spinner loading-lg"></div>
  </div>
{:else}
  <div class="space-y-4">
    <!-- BirdWeather Settings -->
    <SettingsSection
      title={t('settings.integration.birdweather.title')}
      description={t('settings.integration.birdweather.description')}
      defaultOpen={true}
      hasChanges={birdweatherHasChanges}
    >
      <div class="space-y-4">
        <!-- FFmpeg Warning -->
        {#if !ffmpegAvailable}
          <div class="alert alert-warning" role="alert">
            {@html alertIconsSvg.warning}
            <div>
              <h3 class="font-bold">{t('settings.integration.birdweather.ffmpegWarning.title')}</h3>
              <p class="text-sm">
                {t('settings.integration.birdweather.ffmpegWarning.message')}
              </p>
            </div>
          </div>
        {/if}

        <Checkbox
          bind:checked={settings.birdweather!.enabled}
          label={t('settings.integration.birdweather.enable')}
          disabled={store.isLoading || store.isSaving}
          onchange={() => updateBirdWeatherEnabled(settings.birdweather!.enabled)}
        />

        {#if settings.birdweather?.enabled}
          <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
            <PasswordField
              label={t('settings.integration.birdweather.token.label')}
              value={settings.birdweather!.id}
              onUpdate={updateBirdWeatherId}
              placeholder=""
              helpText={t('settings.integration.birdweather.token.helpText')}
              disabled={store.isLoading || store.isSaving}
              allowReveal={true}
            />

            <NumberField
              label={t('settings.integration.birdweather.threshold.label')}
              value={settings.birdweather!.threshold}
              onUpdate={updateBirdWeatherThreshold}
              min={0}
              max={1}
              step={0.01}
              placeholder="0.7"
              helpText={t('settings.integration.birdweather.threshold.helpText')}
              disabled={store.isLoading || store.isSaving}
            />
          </div>

          <!-- Test Connection -->
          <div class="space-y-4">
            <div class="flex items-center gap-3">
              <SettingsButton
                onclick={testBirdWeather}
                loading={testStates.birdweather.isRunning}
                loadingText={t('settings.integration.birdweather.test.loading')}
                disabled={!(
                  store.formData?.realtime?.birdweather?.enabled ?? settings.birdweather?.enabled
                ) ||
                  !(store.formData?.realtime?.birdweather?.id ?? settings.birdweather?.id) ||
                  testStates.birdweather.isRunning}
              >
                {t('settings.integration.birdweather.test.button')}
              </SettingsButton>
              <span class="text-sm text-base-content/70">
                {#if !(store.formData?.realtime?.birdweather?.enabled ?? settings.birdweather?.enabled)}
                  {t('settings.integration.birdweather.test.enabledRequired')}
                {:else if !(store.formData?.realtime?.birdweather?.id ?? settings.birdweather?.id)}
                  {t('settings.integration.birdweather.test.tokenRequired')}
                {:else if testStates.birdweather.isRunning}
                  {t('settings.integration.birdweather.test.inProgress')}
                {:else}
                  {t('settings.integration.birdweather.test.description')}
                {/if}
              </span>
            </div>

            {#if testStates.birdweather.stages.length > 0}
              <MultiStageOperation
                stages={testStates.birdweather.stages}
                variant="compact"
                showProgress={false}
              />
            {/if}

            <TestSuccessNote show={testStates.birdweather.showSuccessNote} />
          </div>
        {/if}
      </div>
    </SettingsSection>

    <!-- MQTT Settings -->
    <SettingsSection
      title={t('settings.integration.mqtt.title')}
      description={t('settings.integration.mqtt.description')}
      defaultOpen={false}
      hasChanges={mqttHasChanges}
    >
      <div class="space-y-4">
        <Checkbox
          bind:checked={settings.mqtt!.enabled}
          label={t('settings.integration.mqtt.enable')}
          disabled={store.isLoading || store.isSaving}
          onchange={() => updateMQTTEnabled(settings.mqtt!.enabled)}
        />

        {#if settings.mqtt?.enabled}
          <div class="space-y-4">
            <TextInput
              id="mqtt-broker"
              bind:value={settings.mqtt!.broker}
              label={t('settings.integration.mqtt.broker.label')}
              placeholder={t('settings.integration.mqtt.broker.placeholder')}
              disabled={store.isLoading || store.isSaving}
              onchange={updateMQTTBroker}
            />

            <TextInput
              id="mqtt-topic"
              bind:value={settings.mqtt!.topic}
              label={t('settings.integration.mqtt.topic.label')}
              placeholder={t('settings.integration.mqtt.topic.placeholder')}
              disabled={store.isLoading || store.isSaving}
              onchange={updateMQTTTopic}
            />

            <!-- Authentication Section -->
            <div class="border-t border-base-300 pt-4 mt-2">
              <h3 class="text-sm font-medium mb-3">
                {t('settings.integration.mqtt.authentication.title')}
              </h3>

              <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
                <TextInput
                  id="mqtt-username"
                  value={settings.mqtt!.username || ''}
                  label={t('settings.integration.mqtt.authentication.username.label')}
                  placeholder=""
                  disabled={store.isLoading || store.isSaving}
                  onchange={value => updateMQTTUsername(value)}
                />

                <PasswordField
                  label={t('settings.integration.mqtt.authentication.password.label')}
                  value={settings.mqtt!.password || ''}
                  onUpdate={updateMQTTPassword}
                  placeholder=""
                  helpText={t('settings.integration.mqtt.authentication.password.helpText')}
                  disabled={store.isLoading || store.isSaving}
                  allowReveal={true}
                />
              </div>
            </div>

            <!-- Message Settings Section -->
            <div class="border-t border-base-300 pt-4 mt-2">
              <h3 class="text-sm font-medium mb-3">
                {t('settings.integration.mqtt.messageSettings.title')}
              </h3>

              <!-- prettier-ignore -->
              <Checkbox
                checked={(settings.mqtt as MQTTSettings).retain ?? false}
                onchange={(checked) => updateMQTTRetain(checked)}
                label={t('settings.integration.mqtt.messageSettings.retain.label')}
                disabled={store.isLoading || store.isSaving}
              />

              <!-- Note about MQTT Retain for HomeAssistant -->
              <SettingsNote>
                <span>{@html t('settings.integration.mqtt.messageSettings.retain.note')}</span>
              </SettingsNote>
            </div>

            <!-- TLS/SSL Security Section -->
            <div class="border-t border-base-300 pt-4 mt-2">
              <h3 class="text-sm font-medium mb-3">{t('settings.integration.mqtt.tls.title')}</h3>

              <Checkbox
                bind:checked={settings.mqtt!.tls.enabled}
                label={t('settings.integration.mqtt.tls.enable')}
                disabled={store.isLoading || store.isSaving}
                onchange={() => updateMQTTTLSEnabled(settings.mqtt!.tls.enabled)}
              />

              {#if settings.mqtt?.tls.enabled}
                <Checkbox
                  bind:checked={settings.mqtt!.tls.skipVerify}
                  label={t('settings.integration.mqtt.tls.skipVerify')}
                  disabled={store.isLoading || store.isSaving}
                  onchange={() => updateMQTTTLSSkipVerify(settings.mqtt!.tls.skipVerify)}
                />

                <div class="alert alert-info">
                  {@html alertIconsSvg.info}
                  <div>
                    <span>{@html t('settings.integration.mqtt.tls.configNote')}</span>
                  </div>
                </div>
              {/if}
            </div>

            <!-- Test Connection -->
            <div class="space-y-4">
              <div class="flex items-center gap-3">
                <SettingsButton
                  onclick={testMQTT}
                  loading={testStates.mqtt.isRunning}
                  loadingText={t('settings.integration.mqtt.test.loading')}
                  disabled={!(store.formData?.realtime?.mqtt?.enabled ?? settings.mqtt?.enabled) ||
                    !(store.formData?.realtime?.mqtt?.broker ?? settings.mqtt?.broker) ||
                    testStates.mqtt.isRunning}
                >
                  {t('settings.integration.mqtt.test.button')}
                </SettingsButton>
                <span class="text-sm text-base-content/70">
                  {#if !(store.formData?.realtime?.mqtt?.enabled ?? settings.mqtt?.enabled)}
                    {t('settings.integration.mqtt.test.enabledRequired')}
                  {:else if !(store.formData?.realtime?.mqtt?.broker ?? settings.mqtt?.broker)}
                    {t('settings.integration.mqtt.test.brokerRequired')}
                  {:else if testStates.mqtt.isRunning}
                    {t('settings.integration.mqtt.test.inProgress')}
                  {:else}
                    {t('settings.integration.mqtt.test.description')}
                  {/if}
                </span>
              </div>

              {#if testStates.mqtt.stages.length > 0}
                <MultiStageOperation
                  stages={testStates.mqtt.stages}
                  variant="compact"
                  showProgress={false}
                />
              {/if}

              <TestSuccessNote show={testStates.mqtt.showSuccessNote} />
            </div>
          </div>
        {/if}
      </div>
    </SettingsSection>

    <!-- Observability Settings -->
    <SettingsSection
      title={t('settings.integration.observability.title')}
      description={t('settings.integration.observability.description')}
      defaultOpen={false}
      hasChanges={observabilityHasChanges}
    >
      <div class="space-y-4">
        <Checkbox
          bind:checked={settings.observability!.prometheus.enabled}
          label={t('settings.integration.observability.enable')}
          disabled={store.isLoading || store.isSaving}
          onchange={() => updateObservabilityEnabled(settings.observability!.prometheus.enabled)}
        />

        {#if settings.observability?.prometheus.enabled}
          <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
            <TextInput
              id="observability-listen"
              value={`0.0.0.0:${settings.observability!.prometheus.port}`}
              label={t('settings.integration.observability.listenAddress.label')}
              placeholder={t('settings.integration.observability.listenAddress.placeholder')}
              disabled={store.isLoading || store.isSaving}
              onchange={updateObservabilityListen}
            />
          </div>
        {/if}
      </div>
    </SettingsSection>

    <!-- Weather Settings -->
    <SettingsSection
      title={t('settings.integration.weather.title')}
      description={t('settings.integration.weather.description')}
      defaultOpen={false}
      hasChanges={weatherHasChanges}
    >
      <div class="space-y-4">
        <SelectField
          id="weather-provider"
          bind:value={settings.weather!.provider}
          label={t('settings.integration.weather.provider.label')}
          options={[
            { value: 'none', label: t('settings.integration.weather.provider.options.none') },
            { value: 'yrno', label: t('settings.integration.weather.provider.options.yrno') },
            {
              value: 'openweather',
              label: t('settings.integration.weather.provider.options.openweather'),
            },
          ]}
          disabled={store.isLoading || store.isSaving}
          onchange={updateWeatherProvider}
        />

        <!-- Provider-specific notes -->
        {#if (settings.weather?.provider as WeatherSettings['provider']) === 'none'}
          <SettingsNote>
            <span>{t('settings.integration.weather.notes.none')}</span>
          </SettingsNote>
        {:else if (settings.weather?.provider as WeatherSettings['provider']) === 'yrno'}
          <SettingsNote>
            <p>
              {t('settings.integration.weather.notes.yrno.description')}
            </p>
            <p class="mt-2">
              {@html t('settings.integration.weather.notes.yrno.freeService')}
            </p>
          </SettingsNote>
        {:else if (settings.weather?.provider as WeatherSettings['provider']) === 'openweather'}
          <SettingsNote>
            <span>{@html t('settings.integration.weather.notes.openweather')}</span>
          </SettingsNote>

          <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
            <PasswordField
              label={t('settings.integration.weather.apiKey.label')}
              value={settings.weather!.openWeather.apiKey || ''}
              onUpdate={updateWeatherApiKey}
              placeholder=""
              helpText={t('settings.integration.weather.apiKey.helpText')}
              disabled={store.isLoading || store.isSaving}
              allowReveal={true}
            />

            <SelectField
              id="weather-units"
              value={settings.weather!.openWeather.units || 'metric'}
              label={t('settings.integration.weather.units.label')}
              options={[
                {
                  value: 'standard',
                  label: t('settings.integration.weather.units.options.standard'),
                },
                { value: 'metric', label: t('settings.integration.weather.units.options.metric') },
                {
                  value: 'imperial',
                  label: t('settings.integration.weather.units.options.imperial'),
                },
              ]}
              disabled={store.isLoading || store.isSaving}
              onchange={updateWeatherUnits}
            />
          </div>
        {/if}

        {#if (settings.weather?.provider as WeatherSettings['provider']) !== 'none'}
          <!-- Test Weather Provider -->
          <div class="space-y-4">
            <div class="flex items-center gap-3">
              <SettingsButton
                onclick={testWeather}
                loading={testStates.weather.isRunning}
                loadingText={t('settings.integration.weather.test.loading')}
                disabled={(store.formData?.realtime?.weather?.provider ??
                  settings.weather?.provider) === 'none' ||
                  ((store.formData?.realtime?.weather?.provider ?? settings.weather?.provider) ===
                    'openweather' &&
                    !(
                      store.formData?.realtime?.weather?.openWeather?.apiKey ??
                      settings.weather?.openWeather?.apiKey
                    )) ||
                  testStates.weather.isRunning}
              >
                {t('settings.integration.weather.test.button')}
              </SettingsButton>
              <span class="text-sm text-base-content/70">
                {#if (store.formData?.realtime?.weather?.provider ?? settings.weather?.provider) === 'none'}
                  {t('settings.integration.weather.test.noProvider')}
                {:else if (store.formData?.realtime?.weather?.provider ?? settings.weather?.provider) === 'openweather' && !(store.formData?.realtime?.weather?.openWeather?.apiKey ?? settings.weather?.openWeather?.apiKey)}
                  {t('settings.integration.weather.test.apiKeyRequired')}
                {:else if testStates.weather.isRunning}
                  {t('settings.integration.weather.test.inProgress')}
                {:else}
                  {t('settings.integration.weather.test.description')}
                {/if}
              </span>
            </div>

            {#if testStates.weather.stages.length > 0}
              <MultiStageOperation
                stages={testStates.weather.stages}
                variant="compact"
                showProgress={false}
              />
            {/if}

            <TestSuccessNote show={testStates.weather.showSuccessNote} />
          </div>
        {/if}
      </div>
    </SettingsSection>
  </div>
{/if}
