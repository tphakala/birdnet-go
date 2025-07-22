<script lang="ts">
  import NumberField from '$lib/desktop/components/forms/NumberField.svelte';
  import Checkbox from '$lib/desktop/components/forms/Checkbox.svelte';
  import SelectField from '$lib/desktop/components/forms/SelectField.svelte';
  import TextInput from '$lib/desktop/components/forms/TextInput.svelte';
  import PasswordField from '$lib/desktop/components/forms/PasswordField.svelte';
  import SettingsSection from '$lib/desktop/components/ui/SettingsSection.svelte';
  import MultiStageOperation from '$lib/desktop/components/ui/MultiStageOperation.svelte';
  import SettingsButton from '$lib/desktop/components/ui/SettingsButton.svelte';
  import SettingsNote from '$lib/desktop/components/ui/SettingsNote.svelte';
  import TestSuccessNote from '$lib/desktop/components/ui/TestSuccessNote.svelte';
  import { alertIconsSvg } from '$lib/utils/icons'; // Centralized icons - see icons.ts
  import {
    settingsStore,
    settingsActions,
    integrationSettings,
    realtimeSettings,
    type SettingsFormData,
    type BirdWeatherSettings,
    type MQTTSettings,
    type WeatherSettings,
    type ObservabilitySettings,
  } from '$lib/stores/settings';
  import { hasSettingsChanged } from '$lib/utils/settingsChanges';
  import type { Stage } from '$lib/desktop/components/ui/MultiStageOperation.types';
  import { getCsrfToken } from '$lib/utils/api.js';

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

  // Weather provider options
  const weatherProviderOptions = [
    { value: 'none', label: 'None' },
    { value: 'yrno', label: 'Yr.no' },
    { value: 'openweather', label: 'OpenWeather' },
  ];

  // OpenWeather units options
  const openWeatherUnitsOptions = [
    { value: 'standard', label: 'Standard' },
    { value: 'metric', label: 'Metric' },
    { value: 'imperial', label: 'Imperial' },
  ];

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
    console.log('Starting BirdWeather test...');
    testStates.birdweather.isRunning = true;
    testStates.birdweather.stages = [];

    try {
      // Get current form values (unsaved changes) instead of saved settings
      const currentBirdweather = store.formData?.realtime?.birdweather || settings.birdweather!;
      console.log('BirdWeather test config:', currentBirdweather);

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

      console.log('Sending BirdWeather test request with payload:', testPayload);

      const response = await fetch('/api/v2/integrations/birdweather/test', {
        method: 'POST',
        headers,
        credentials: 'same-origin',
        body: JSON.stringify(testPayload),
      });

      console.log('BirdWeather test response status:', response.status, response.statusText);

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
        console.log('Raw BirdWeather chunk received:', chunk);

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
            console.error('Error splitting JSON objects:', e);
            break;
          }
        }

        for (const jsonStr of jsonObjects) {
          try {
            const stageResult = JSON.parse(jsonStr);
            console.log('BirdWeather test result received:', stageResult);

            // Handle initial failure responses that don't have a stage
            if (!stageResult.stage) {
              // If this is a failed result without stages, show it as an error
              if (stageResult.success === false && stageResult.message) {
                console.log('Handling initial error response:', stageResult);
                testStates.birdweather.stages.push({
                  id: 'initial-error',
                  title: 'Configuration Check',
                  status: 'error',
                  message: stageResult.message,
                  error: stageResult.message,
                });
              } else {
                console.log('Skipping result without stage:', stageResult);
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

            console.log('Adding/updating BirdWeather stage:', stage);

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

            console.log('Current BirdWeather stages:', testStates.birdweather.stages);
          } catch (parseError) {
            console.error('Failed to parse BirdWeather test result:', parseError, jsonStr);
          }
        }
      }
    } catch (error) {
      console.error('BirdWeather test failed:', error);

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
      console.log('BirdWeather test finished, stages:', testStates.birdweather.stages);

      // Check if all stages completed successfully and there are unsaved changes
      const allStagesCompleted =
        testStates.birdweather.stages.length > 0 &&
        testStates.birdweather.stages.every(stage => stage.status === 'completed');
      testStates.birdweather.showSuccessNote = allStagesCompleted && birdweatherHasChanges;

      // Increase timeout to 30 seconds so users can see the results
      setTimeout(() => {
        console.log('Clearing BirdWeather test results after timeout');
        testStates.birdweather.stages = [];
        testStates.birdweather.showSuccessNote = false;
      }, 30000);
    }
  }

  async function testMQTT() {
    console.log('Starting MQTT test...');
    testStates.mqtt.isRunning = true;
    testStates.mqtt.stages = [];

    try {
      // Get current form values (unsaved changes) instead of saved settings
      const currentMqtt = store.formData?.realtime?.mqtt || settings.mqtt!;
      console.log('MQTT test config:', currentMqtt);

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

      console.log('Sending MQTT test request with payload:', testPayload);

      const response = await fetch('/api/v2/integrations/mqtt/test', {
        method: 'POST',
        headers,
        credentials: 'same-origin',
        body: JSON.stringify(testPayload),
      });

      console.log('MQTT test response status:', response.status, response.statusText);

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
        console.log('Raw MQTT chunk received:', chunk);

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
            console.error('Error splitting JSON objects:', e);
            break;
          }
        }

        for (const jsonStr of jsonObjects) {
          try {
            const stageResult = JSON.parse(jsonStr);
            console.log('MQTT test result received:', stageResult);

            // Skip results that don't have a stage (like elapsed time info)
            if (!stageResult.stage) {
              console.log('Skipping result without stage:', stageResult);
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

            console.log('Adding/updating MQTT stage:', stage);

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

            console.log('Current MQTT stages:', testStates.mqtt.stages);
          } catch (parseError) {
            console.error('Failed to parse MQTT test result:', parseError, jsonStr);
          }
        }
      }
    } catch (error) {
      console.error('MQTT test failed:', error);

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
      console.log('MQTT test finished, stages:', testStates.mqtt.stages);

      // Check if all stages completed successfully and there are unsaved changes
      const allStagesCompleted =
        testStates.mqtt.stages.length > 0 &&
        testStates.mqtt.stages.every(stage => stage.status === 'completed');
      testStates.mqtt.showSuccessNote = allStagesCompleted && mqttHasChanges;

      // Increase timeout to 30 seconds so users can see the results
      setTimeout(() => {
        console.log('Clearing MQTT test results after timeout');
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
            console.error('Failed to parse stage result:', parseError, line);
          }
        }
      }
    } catch (error) {
      console.error('Weather test failed:', error);

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
      title="BirdWeather"
      description="Upload detections to BirdWeather"
      defaultOpen={true}
      hasChanges={birdweatherHasChanges}
    >
      <div class="space-y-4">
        <!-- FFmpeg Warning -->
        {#if !ffmpegAvailable}
          <div class="alert alert-warning" role="alert">
            {@html alertIconsSvg.warning}
            <div>
              <h3 class="font-bold">FFmpeg not detected</h3>
              <p class="text-sm">
                Please install FFmpeg to enable FLAC encoding support, BirdWeather is deprecating
                WAV uploads in favor of compressed FLAC audio files.
              </p>
            </div>
          </div>
        {/if}

        <Checkbox
          bind:checked={settings.birdweather!.enabled}
          label="Enable BirdWeather Uploads"
          disabled={store.isLoading || store.isSaving}
          onchange={() => updateBirdWeatherEnabled(settings.birdweather!.enabled)}
        />

        {#if settings.birdweather?.enabled}
          <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
            <PasswordField
              label="BirdWeather token"
              value={settings.birdweather!.id}
              onUpdate={updateBirdWeatherId}
              placeholder=""
              helpText="Your unique BirdWeather token."
              disabled={store.isLoading || store.isSaving}
              allowReveal={true}
            />

            <NumberField
              label="Upload Threshold"
              value={settings.birdweather!.threshold}
              onUpdate={updateBirdWeatherThreshold}
              min={0}
              max={1}
              step={0.01}
              placeholder="0.7"
              helpText="Minimum confidence threshold for uploading predictions to BirdWeather."
              disabled={store.isLoading || store.isSaving}
            />
          </div>

          <!-- Test Connection -->
          <div class="space-y-4">
            <div class="flex items-center gap-3">
              <SettingsButton
                onclick={testBirdWeather}
                loading={testStates.birdweather.isRunning}
                loadingText="Testing..."
                disabled={!(
                  store.formData?.realtime?.birdweather?.enabled ?? settings.birdweather?.enabled
                ) ||
                  !(store.formData?.realtime?.birdweather?.id ?? settings.birdweather?.id) ||
                  testStates.birdweather.isRunning}
              >
                Test BirdWeather Connection
              </SettingsButton>
              <span class="text-sm text-base-content/70">
                {#if !(store.formData?.realtime?.birdweather?.enabled ?? settings.birdweather?.enabled)}
                  BirdWeather must be enabled to test
                {:else if !(store.formData?.realtime?.birdweather?.id ?? settings.birdweather?.id)}
                  BirdWeather token must be specified
                {:else if testStates.birdweather.isRunning}
                  Test in progress...
                {:else}
                  Test BirdWeather connection
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
      title="MQTT"
      description="Configure MQTT broker connection"
      defaultOpen={false}
      hasChanges={mqttHasChanges}
    >
      <div class="space-y-4">
        <Checkbox
          bind:checked={settings.mqtt!.enabled}
          label="Enable MQTT Integration"
          disabled={store.isLoading || store.isSaving}
          onchange={() => updateMQTTEnabled(settings.mqtt!.enabled)}
        />

        {#if settings.mqtt?.enabled}
          <div class="space-y-4">
            <TextInput
              id="mqtt-broker"
              bind:value={settings.mqtt!.broker}
              label="MQTT Broker"
              placeholder="mqtt://localhost:1883"
              disabled={store.isLoading || store.isSaving}
              onchange={updateMQTTBroker}
            />

            <TextInput
              id="mqtt-topic"
              bind:value={settings.mqtt!.topic}
              label="MQTT Topic"
              placeholder="birdnet/detections"
              disabled={store.isLoading || store.isSaving}
              onchange={updateMQTTTopic}
            />

            <!-- Authentication Section -->
            <div class="border-t border-base-300 pt-4 mt-2">
              <h3 class="text-sm font-medium mb-3">Authentication</h3>

              <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
                <TextInput
                  id="mqtt-username"
                  value={settings.mqtt!.username || ''}
                  label="Username"
                  placeholder=""
                  disabled={store.isLoading || store.isSaving}
                  onchange={value => updateMQTTUsername(value)}
                />

                <PasswordField
                  label="Password"
                  value={settings.mqtt!.password || ''}
                  onUpdate={updateMQTTPassword}
                  placeholder=""
                  helpText="The MQTT password."
                  disabled={store.isLoading || store.isSaving}
                  allowReveal={true}
                />
              </div>
            </div>

            <!-- Message Settings Section -->
            <div class="border-t border-base-300 pt-4 mt-2">
              <h3 class="text-sm font-medium mb-3">Message Settings</h3>

              <Checkbox
                checked={(settings.mqtt as MQTTSettings).retain || false}
                label="Retain Messages"
                disabled={store.isLoading || store.isSaving}
                onchange={(checked) => updateMQTTRetain(checked)}
              />

              <!-- Note about MQTT Retain for HomeAssistant -->
              <SettingsNote>
                <span
                  ><strong>Home Assistant Users:</strong> It's recommended to enable the retain flag
                  for Home Assistant integration. Without retain, MQTT sensors will appear as 'unknown'
                  when Home Assistant restarts.</span
                >
              </SettingsNote>
            </div>

            <!-- TLS/SSL Security Section -->
            <div class="border-t border-base-300 pt-4 mt-2">
              <h3 class="text-sm font-medium mb-3">TLS/SSL Security</h3>

              <Checkbox
                bind:checked={settings.mqtt!.tls.enabled}
                label="Enable TLS/SSL"
                disabled={store.isLoading || store.isSaving}
                onchange={() => updateMQTTTLSEnabled(settings.mqtt!.tls.enabled)}
              />

              {#if settings.mqtt?.tls.enabled}
                <Checkbox
                  bind:checked={settings.mqtt!.tls.skipVerify}
                  label="Skip Certificate Verification"
                  disabled={store.isLoading || store.isSaving}
                  onchange={() => updateMQTTTLSSkipVerify(settings.mqtt!.tls.skipVerify)}
                />

                <div class="alert alert-info">
                  {@html alertIconsSvg.info}
                  <div>
                    <span
                      ><strong>TLS Configuration:</strong><br />• Standard TLS: Leave certificates
                      empty for public brokers<br />• Self-signed certificates: Provide CA
                      Certificate<br />• Mutual TLS (mTLS): Provide all three certificates</span
                    >
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
                  loadingText="Testing..."
                  disabled={!(store.formData?.realtime?.mqtt?.enabled ?? settings.mqtt?.enabled) ||
                    !(store.formData?.realtime?.mqtt?.broker ?? settings.mqtt?.broker) ||
                    testStates.mqtt.isRunning}
                >
                  Test MQTT Connection
                </SettingsButton>
                <span class="text-sm text-base-content/70">
                  {#if !(store.formData?.realtime?.mqtt?.enabled ?? settings.mqtt?.enabled)}
                    MQTT must be enabled to test
                  {:else if !(store.formData?.realtime?.mqtt?.broker ?? settings.mqtt?.broker)}
                    MQTT broker must be specified
                  {:else if testStates.mqtt.isRunning}
                    Test in progress...
                  {:else}
                    Test MQTT connection
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
      title="Observability"
      description="Monitor BirdNET-Go's performance and bird detection metrics through Prometheus-compatible endpoint"
      defaultOpen={false}
      hasChanges={observabilityHasChanges}
    >
      <div class="space-y-4">
        <Checkbox
          bind:checked={settings.observability!.prometheus.enabled}
          label="Enable Observability Integration"
          disabled={store.isLoading || store.isSaving}
          onchange={() => updateObservabilityEnabled(settings.observability!.prometheus.enabled)}
        />

        {#if settings.observability?.prometheus.enabled}
          <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
            <TextInput
              id="observability-listen"
              value={`0.0.0.0:${settings.observability!.prometheus.port}`}
              label="Listen Address"
              placeholder="0.0.0.0:8090"
              disabled={store.isLoading || store.isSaving}
              onchange={updateObservabilityListen}
            />
          </div>
        {/if}
      </div>
    </SettingsSection>

    <!-- Weather Settings -->
    <SettingsSection
      title="Weather"
      description="Configure weather data collection"
      defaultOpen={false}
      hasChanges={weatherHasChanges}
    >
      <div class="space-y-4">
        <SelectField
          id="weather-provider"
          bind:value={settings.weather!.provider}
          label="Weather Provider"
          options={weatherProviderOptions}
          disabled={store.isLoading || store.isSaving}
          onchange={updateWeatherProvider}
        />

        <!-- Provider-specific notes -->
        {#if (settings.weather?.provider as WeatherSettings['provider']) === 'none'}
          <SettingsNote>
            <span>No weather data will be retrieved.</span>
          </SettingsNote>
        {:else if (settings.weather?.provider as WeatherSettings['provider']) === 'yrno'}
          <SettingsNote>
            <p>
              Weather forecast data is provided by Yr.no, a joint service by the Norwegian
              Meteorological Institute (met.no) and the Norwegian Broadcasting Corporation (NRK).
            </p>
            <p class="mt-2">
              Yr is a free weather data service. For more information, visit <a
                href="https://hjelp.yr.no/hc/en-us/articles/206550539-Facts-about-Yr"
                class="link link-primary"
                target="_blank"
                rel="noopener noreferrer">Yr.no</a
              >.
            </p>
          </SettingsNote>
        {:else if (settings.weather?.provider as WeatherSettings['provider']) === 'openweather'}
          <SettingsNote>
            <span
              >Use of OpenWeather requires an API key, sign up for a free API key at <a
                href="https://home.openweathermap.org/users/sign_up"
                class="link link-primary"
                target="_blank"
                rel="noopener noreferrer">OpenWeather</a
              >.</span
            >
          </SettingsNote>

          <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
            <PasswordField
              label="API Key"
              value={settings.weather!.openWeather.apiKey || ''}
              onUpdate={updateWeatherApiKey}
              placeholder=""
              helpText="Your OpenWeather API key. Keep this secret!"
              disabled={store.isLoading || store.isSaving}
              allowReveal={true}
            />

            <SelectField
              id="weather-units"
              value={settings.weather!.openWeather.units || 'metric'}
              label="Units of Measurement"
              options={openWeatherUnitsOptions}
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
                loadingText="Testing..."
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
                Test Weather Provider
              </SettingsButton>
              <span class="text-sm text-base-content/70">
                {#if (store.formData?.realtime?.weather?.provider ?? settings.weather?.provider) === 'none'}
                  No weather provider selected
                {:else if (store.formData?.realtime?.weather?.provider ?? settings.weather?.provider) === 'openweather' && !(store.formData?.realtime?.weather?.openWeather?.apiKey ?? settings.weather?.openWeather?.apiKey)}
                  OpenWeather API key must be specified
                {:else if testStates.weather.isRunning}
                  Test in progress...
                {:else}
                  Test weather provider connection
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
