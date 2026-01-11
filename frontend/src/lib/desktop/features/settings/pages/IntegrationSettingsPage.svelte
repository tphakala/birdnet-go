<!--
  Integration Settings Page Component
  
  Purpose: Configure external service integrations for BirdNET-Go including BirdWeather,
  MQTT, observability (Prometheus), and weather provider integrations.
  
  Features:
  - BirdWeather integration with threshold settings and connection testing
  - MQTT broker configuration with authentication and TLS support
  - Prometheus metrics endpoint configuration
  - Weather provider selection (YR.no, OpenWeather) with API testing
  - Multi-stage operation feedback for connection testing
  - Real-time validation and change detection
  
  Props: None - This is a page component that uses global settings stores
  
  Performance Optimizations:
  - Removed page-level loading spinner to prevent flickering
  - Cached CSRF token to avoid repeated DOM queries
  - Reactive change detection with $derived
  - Efficient state management for test operations
  - Streaming response handling for test endpoints
  
  @component
-->
<script lang="ts">
  import Checkbox from '$lib/desktop/components/forms/Checkbox.svelte';
  import NumberField from '$lib/desktop/components/forms/NumberField.svelte';
  import PasswordField from '$lib/desktop/components/forms/PasswordField.svelte';
  import TextInput from '$lib/desktop/components/forms/TextInput.svelte';
  import MultiStageOperation from '$lib/desktop/components/ui/MultiStageOperation.svelte';
  import type { Stage } from '$lib/desktop/components/ui/MultiStageOperation.types';
  import TestSuccessNote from '$lib/desktop/components/ui/TestSuccessNote.svelte';
  import SettingsButton from '$lib/desktop/features/settings/components/SettingsButton.svelte';
  import SettingsNote from '$lib/desktop/features/settings/components/SettingsNote.svelte';
  import SettingsSection from '$lib/desktop/features/settings/components/SettingsSection.svelte';
  import SettingsTabs from '$lib/desktop/features/settings/components/SettingsTabs.svelte';
  import type { TabDefinition } from '$lib/desktop/features/settings/components/SettingsTabs.svelte';
  import { t } from '$lib/i18n';
  import { Bird, Radio, Activity } from '@lucide/svelte';
  import {
    integrationSettings,
    realtimeSettings,
    settingsActions,
    settingsStore,
    type MQTTSettings,
    type SettingsFormData,
  } from '$lib/stores/settings';
  import { TriangleAlert, Info, Send } from '@lucide/svelte';
  import { toastActions } from '$lib/stores/toast';
  import { loggers } from '$lib/utils/logger';
  import { safeArrayAccess } from '$lib/utils/security';
  import { hasSettingsChanged } from '$lib/utils/settingsChanges';
  import { getCsrfToken } from '$lib/utils/api';

  const logger = loggers.settings;

  // PERFORMANCE OPTIMIZATION: Reactive settings with proper defaults
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
        homeAssistant: {
          enabled: false,
          discoveryPrefix: 'homeassistant',
          deviceName: 'BirdNET-Go',
        },
      },
      observability: {
        prometheus: {
          enabled: false,
          port: 9090,
          path: '/metrics',
        },
      },
    }
  );

  let store = $derived($settingsStore);

  // PERFORMANCE OPTIMIZATION: Reactive change detection with $derived
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

  // Test states for multi-stage operations
  let testStates = $state<{
    birdweather: { stages: Stage[]; isRunning: boolean; showSuccessNote: boolean };
    mqtt: { stages: Stage[]; isRunning: boolean; showSuccessNote: boolean };
  }>({
    birdweather: { stages: [], isRunning: false, showSuccessNote: false },
    mqtt: { stages: [], isRunning: false, showSuccessNote: false },
  });

  // FFmpeg availability check
  let ffmpegAvailable = $state(true);

  // Tab state
  let activeTab = $state('birdweather');

  // Tab definitions
  let tabs = $derived<TabDefinition[]>([
    {
      id: 'birdweather',
      label: t('settings.integration.birdweather.title'),
      icon: Bird,
      content: birdweatherTabContent,
      hasChanges: birdweatherHasChanges,
    },
    {
      id: 'mqtt',
      label: t('settings.integration.mqtt.title'),
      icon: Radio,
      content: mqttTabContent,
      hasChanges: mqttHasChanges,
    },
    {
      id: 'prometheus',
      label: t('settings.integration.observability.title'),
      icon: Activity,
      content: prometheusTabContent,
      hasChanges: observabilityHasChanges,
    },
  ]);

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

  // Home Assistant default settings constant (DRY principle)
  const DEFAULT_HOME_ASSISTANT_SETTINGS = {
    enabled: false,
    discoveryPrefix: 'homeassistant',
    deviceName: 'BirdNET-Go',
  };

  // Generic Home Assistant update function to reduce duplication
  function updateMQTTHomeAssistant<K extends keyof typeof DEFAULT_HOME_ASSISTANT_SETTINGS>(
    field: K,
    value: (typeof DEFAULT_HOME_ASSISTANT_SETTINGS)[K]
  ) {
    settingsActions.updateSection('realtime', {
      mqtt: {
        ...(settings.mqtt as MQTTSettings),
        homeAssistant: {
          ...(settings.mqtt?.homeAssistant ?? DEFAULT_HOME_ASSISTANT_SETTINGS),
          [field]: value,
        },
      },
    });
  }

  // Home Assistant update handlers (wrappers for type-safe binding in templates)
  function updateMQTTHomeAssistantEnabled(enabled: boolean) {
    updateMQTTHomeAssistant('enabled', enabled);
  }

  function updateMQTTHomeAssistantPrefix(discoveryPrefix: string) {
    updateMQTTHomeAssistant('discoveryPrefix', discoveryPrefix);
  }

  function updateMQTTHomeAssistantDeviceName(deviceName: string) {
    updateMQTTHomeAssistant('deviceName', deviceName);
  }

  // Home Assistant discovery state and handler
  let isSendingDiscovery = $state(false);

  async function handleSendDiscovery() {
    if (mqttHasChanges) {
      // Don't send discovery with unsaved changes
      return;
    }

    isSendingDiscovery = true;
    try {
      const response = await fetch('/api/v2/integrations/mqtt/homeassistant/discovery', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'X-CSRF-Token': getCsrfToken() || '',
        },
      });

      if (response.ok) {
        toastActions.success(t('settings.integration.mqtt.homeAssistant.discovery.success'));
      } else {
        const result = await response.json();
        toastActions.error(
          result.message || t('settings.integration.mqtt.homeAssistant.discovery.error')
        );
      }
    } catch (err) {
      logger.error('Failed to send Home Assistant discovery', err);
      toastActions.error(t('settings.integration.mqtt.homeAssistant.discovery.error'));
    } finally {
      isSendingDiscovery = false;
    }
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

      const token = getCsrfToken();
      if (token) {
        headers.set('X-CSRF-Token', token);
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
              // eslint-disable-next-line security/detect-object-injection
              const char = remaining[i] || '';
              if (char === '{') braceCount++;
              if (char === '}') braceCount--;
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
              // Update existing stage safely
              const existingStage = safeArrayAccess(testStates.birdweather.stages, existingIndex);
              if (
                existingStage &&
                existingIndex >= 0 &&
                existingIndex < testStates.birdweather.stages.length
              ) {
                testStates.birdweather.stages.splice(existingIndex, 1, {
                  ...existingStage,
                  ...stage,
                });
              }
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
        const lastIndex = testStates.birdweather.stages.length - 1;
        const lastStage = safeArrayAccess(testStates.birdweather.stages, lastIndex);
        if (lastStage && lastStage.status !== 'completed') {
          const updatedStage = {
            ...lastStage,
            status: 'error' as const,
            error: error instanceof Error ? error.message : 'Unknown error occurred',
          };
          testStates.birdweather.stages.splice(lastIndex, 1, updatedStage);
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

      const token = getCsrfToken();
      if (token) {
        headers.set('X-CSRF-Token', token);
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
              // eslint-disable-next-line security/detect-object-injection
              const char = remaining[i] || '';
              if (char === '{') braceCount++;
              if (char === '}') braceCount--;
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
              // Update existing stage safely
              const existingStage = safeArrayAccess(testStates.mqtt.stages, existingIndex);
              if (
                existingStage &&
                existingIndex >= 0 &&
                existingIndex < testStates.mqtt.stages.length
              ) {
                testStates.mqtt.stages.splice(existingIndex, 1, {
                  ...existingStage,
                  ...stage,
                });
              }
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
        const lastIndex = testStates.mqtt.stages.length - 1;
        const lastStage = safeArrayAccess(testStates.mqtt.stages, lastIndex);
        if (lastStage && lastStage.status !== 'completed') {
          const updatedStage = {
            ...lastStage,
            status: 'error' as const,
            error: error instanceof Error ? error.message : 'Unknown error occurred',
          };
          testStates.mqtt.stages.splice(lastIndex, 1, updatedStage);
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
</script>

{#snippet birdweatherTabContent()}
  <div class="space-y-6">
    <!-- BirdWeather Settings Card -->
    <SettingsSection
      title={t('settings.integration.birdweather.title')}
      description={t('settings.integration.birdweather.description')}
      originalData={(store.originalData as SettingsFormData)?.realtime?.birdweather}
      currentData={(store.formData as SettingsFormData)?.realtime?.birdweather}
    >
      <div class="space-y-4">
        <!-- FFmpeg Warning -->
        {#if !ffmpegAvailable}
          <div class="alert alert-warning" role="alert">
            <TriangleAlert class="size-5" />
            <div>
              <h3 class="font-bold">{t('settings.integration.birdweather.ffmpegWarning.title')}</h3>
              <p class="text-sm">
                {t('settings.integration.birdweather.ffmpegWarning.message')}
              </p>
            </div>
          </div>
        {/if}

        <Checkbox
          checked={settings.birdweather!.enabled}
          label={t('settings.integration.birdweather.enable')}
          disabled={store.isLoading || store.isSaving}
          onchange={updateBirdWeatherEnabled}
        />

        <!-- Fieldset for accessible disabled state - all inputs greyed out when feature disabled -->
        <fieldset
          disabled={!settings.birdweather?.enabled || store.isLoading || store.isSaving}
          class="contents"
          aria-describedby="birdweather-status"
        >
          <span id="birdweather-status" class="sr-only">
            {settings.birdweather?.enabled
              ? t('settings.integration.birdweather.enable')
              : t('settings.integration.birdweather.test.enabledRequired')}
          </span>
          <div
            class="transition-opacity duration-200"
            class:opacity-50={!settings.birdweather?.enabled}
          >
            <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
              <PasswordField
                label={t('settings.integration.birdweather.token.label')}
                value={settings.birdweather!.id}
                onUpdate={updateBirdWeatherId}
                placeholder=""
                helpText={t('settings.integration.birdweather.token.helpText')}
                disabled={!settings.birdweather?.enabled || store.isLoading || store.isSaving}
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
                disabled={!settings.birdweather?.enabled || store.isLoading || store.isSaving}
              />
            </div>

            <!-- Test Connection -->
            <div class="space-y-4 mt-4">
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
                <span class="text-sm text-base-content opacity-70">
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
          </div>
        </fieldset>
      </div>
    </SettingsSection>
  </div>
{/snippet}

{#snippet mqttTabContent()}
  <div class="space-y-6">
    <!-- MQTT Settings Card -->
    <SettingsSection
      title={t('settings.integration.mqtt.title')}
      description={t('settings.integration.mqtt.description')}
      originalData={(store.originalData as SettingsFormData)?.realtime?.mqtt}
      currentData={(store.formData as SettingsFormData)?.realtime?.mqtt}
    >
      <div class="space-y-4">
        <Checkbox
          checked={settings.mqtt!.enabled}
          label={t('settings.integration.mqtt.enable')}
          disabled={store.isLoading || store.isSaving}
          onchange={updateMQTTEnabled}
        />

        <!-- Fieldset for accessible disabled state - all inputs greyed out when feature disabled -->
        <fieldset
          disabled={!settings.mqtt?.enabled || store.isLoading || store.isSaving}
          class="contents"
          aria-describedby="mqtt-status"
        >
          <span id="mqtt-status" class="sr-only">
            {settings.mqtt?.enabled
              ? t('settings.integration.mqtt.enable')
              : t('settings.integration.mqtt.test.enabledRequired')}
          </span>
          <div
            class="space-y-4 transition-opacity duration-200"
            class:opacity-50={!settings.mqtt?.enabled}
          >
            <TextInput
              id="mqtt-broker"
              value={settings.mqtt!.broker}
              label={t('settings.integration.mqtt.broker.label')}
              placeholder={t('settings.integration.mqtt.broker.placeholder')}
              disabled={!settings.mqtt?.enabled || store.isLoading || store.isSaving}
              onchange={updateMQTTBroker}
            />

            <!-- TLS/SSL Security -->
            <div class="flex flex-col gap-2">
              <Checkbox
                checked={settings.mqtt?.tls?.enabled ?? false}
                label={t('settings.integration.mqtt.tls.enable')}
                disabled={!settings.mqtt?.enabled || store.isLoading || store.isSaving}
                onchange={updateMQTTTLSEnabled}
              />

              {#if settings.mqtt?.tls?.enabled}
                <Checkbox
                  checked={settings.mqtt?.tls?.skipVerify ?? false}
                  label={t('settings.integration.mqtt.tls.skipVerify')}
                  disabled={!settings.mqtt?.enabled || store.isLoading || store.isSaving}
                  onchange={updateMQTTTLSSkipVerify}
                />

                <div class="alert alert-info">
                  <Info class="size-5" />
                  <div>
                    <span>{@html t('settings.integration.mqtt.tls.configNote')}</span>
                  </div>
                </div>
              {/if}
            </div>

            <TextInput
              id="mqtt-topic"
              value={settings.mqtt!.topic}
              label={t('settings.integration.mqtt.topic.label')}
              placeholder={t('settings.integration.mqtt.topic.placeholder')}
              disabled={!settings.mqtt?.enabled || store.isLoading || store.isSaving}
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
                  disabled={!settings.mqtt?.enabled || store.isLoading || store.isSaving}
                  onchange={value => updateMQTTUsername(value)}
                />

                <PasswordField
                  label={t('settings.integration.mqtt.authentication.password.label')}
                  value={settings.mqtt!.password || ''}
                  onUpdate={updateMQTTPassword}
                  placeholder=""
                  helpText={t('settings.integration.mqtt.authentication.password.helpText')}
                  disabled={!settings.mqtt?.enabled || store.isLoading || store.isSaving}
                  allowReveal={true}
                />
              </div>
            </div>

            <!-- Home Assistant Integration Section -->
            <div class="border-t border-base-300 pt-4 mt-2">
              <h3 class="text-sm font-medium mb-3">
                {t('settings.integration.mqtt.homeAssistant.title')}
              </h3>

              <Checkbox
                checked={settings.mqtt?.homeAssistant?.enabled ?? false}
                label={t('settings.integration.mqtt.homeAssistant.enable')}
                disabled={!settings.mqtt?.enabled || store.isLoading || store.isSaving}
                onchange={updateMQTTHomeAssistantEnabled}
              />

              <SettingsNote>
                <span>{@html t('settings.integration.mqtt.homeAssistant.description')}</span>
              </SettingsNote>

              {#if settings.mqtt?.homeAssistant?.enabled}
                <!-- Retain Messages for Home Assistant -->
                <div class="mt-4">
                  <!-- prettier-ignore -->
                  <Checkbox
                    checked={(settings.mqtt as MQTTSettings).retain ?? false}
                    onchange={(checked) => updateMQTTRetain(checked)}
                    label={t('settings.integration.mqtt.homeAssistant.retain.label')}
                    disabled={!settings.mqtt?.enabled || store.isLoading || store.isSaving}
                  />

                  <SettingsNote>
                    <span>{@html t('settings.integration.mqtt.homeAssistant.retain.note')}</span>
                  </SettingsNote>
                </div>

                <div class="grid grid-cols-1 md:grid-cols-2 gap-4 mt-4">
                  <TextInput
                    id="mqtt-ha-prefix"
                    value={settings.mqtt?.homeAssistant?.discoveryPrefix ?? 'homeassistant'}
                    label={t('settings.integration.mqtt.homeAssistant.discoveryPrefix.label')}
                    placeholder="homeassistant"
                    disabled={!settings.mqtt?.enabled || store.isLoading || store.isSaving}
                    onchange={updateMQTTHomeAssistantPrefix}
                  />

                  <TextInput
                    id="mqtt-ha-device-name"
                    value={settings.mqtt?.homeAssistant?.deviceName ?? 'BirdNET-Go'}
                    label={t('settings.integration.mqtt.homeAssistant.deviceName.label')}
                    placeholder="BirdNET-Go"
                    disabled={!settings.mqtt?.enabled || store.isLoading || store.isSaving}
                    onchange={updateMQTTHomeAssistantDeviceName}
                  />
                </div>

                <div class="alert alert-info mt-4">
                  <Info class="size-5" />
                  <div>
                    <span>{@html t('settings.integration.mqtt.homeAssistant.sensorsNote')}</span>
                  </div>
                </div>

                <div class="flex items-center gap-4 mt-4">
                  <button
                    class="btn btn-outline btn-sm"
                    disabled={!settings.mqtt?.enabled ||
                      store.isLoading ||
                      store.isSaving ||
                      isSendingDiscovery ||
                      mqttHasChanges}
                    onclick={handleSendDiscovery}
                  >
                    {#if isSendingDiscovery}
                      <span class="loading loading-spinner loading-xs"></span>
                    {:else}
                      <Send class="size-4" />
                    {/if}
                    {t('settings.integration.mqtt.homeAssistant.discovery.button')}
                  </button>

                  {#if mqttHasChanges}
                    <span class="text-sm text-warning">
                      {t('settings.integration.mqtt.homeAssistant.discovery.saveFirst')}
                    </span>
                  {/if}
                </div>
              {/if}
            </div>

            <!-- Test Connection -->
            <div class="border-t border-base-300 pt-4 mt-2 space-y-4">
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
                <span class="text-sm text-base-content opacity-70">
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
        </fieldset>
      </div>
    </SettingsSection>
  </div>
{/snippet}

{#snippet prometheusTabContent()}
  <div class="space-y-6">
    <!-- Observability Settings Card -->
    <SettingsSection
      title={t('settings.integration.observability.title')}
      description={t('settings.integration.observability.description')}
      originalData={(store.originalData as SettingsFormData)?.realtime?.telemetry}
      currentData={(store.formData as SettingsFormData)?.realtime?.telemetry}
    >
      <div class="space-y-4">
        <Checkbox
          checked={settings.observability!.prometheus.enabled}
          label={t('settings.integration.observability.enable')}
          disabled={store.isLoading || store.isSaving}
          onchange={updateObservabilityEnabled}
        />

        <!-- Fieldset for accessible disabled state - all inputs greyed out when feature disabled -->
        <fieldset
          disabled={!settings.observability?.prometheus.enabled ||
            store.isLoading ||
            store.isSaving}
          class="contents"
          aria-describedby="prometheus-status"
        >
          <span id="prometheus-status" class="sr-only">
            {settings.observability?.prometheus.enabled
              ? t('settings.integration.observability.enable')
              : t('settings.integration.observability.disabled')}
          </span>
          <div
            class="transition-opacity duration-200"
            class:opacity-50={!settings.observability?.prometheus.enabled}
          >
            <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
              <TextInput
                id="observability-listen"
                value={`0.0.0.0:${settings.observability!.prometheus.port}`}
                label={t('settings.integration.observability.listenAddress.label')}
                placeholder={t('settings.integration.observability.listenAddress.placeholder')}
                disabled={!settings.observability?.prometheus.enabled ||
                  store.isLoading ||
                  store.isSaving}
                onchange={updateObservabilityListen}
              />
            </div>
          </div>
        </fieldset>
      </div>
    </SettingsSection>
  </div>
{/snippet}

<!-- Main Content -->
<main class="settings-page-content" aria-label="Integration settings configuration">
  <SettingsTabs {tabs} bind:activeTab />
</main>
