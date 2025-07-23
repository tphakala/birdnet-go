import { t } from './store.svelte.js';
import type { MessageKey } from './types.js';

// Create message functions that match Paraglide's API
export const messages = new Proxy({} as Record<MessageKey, () => string>, {
  get(target, prop) {
    if (typeof prop === 'string') {
      return () => t(prop as MessageKey);
    }
    return undefined;
  },
});

// Shorter alias (matches current usage: m.hero_title())
export const m = messages;

// Export individual message functions for compatibility
export const hero_title = () => t('hero_title');
export const hero_subtitle = () => t('hero_subtitle');
export const hero_description = () => t('hero_description');
export const cta_get_started = () => t('cta_get_started');
export const cta_github = () => t('cta_github');
export const features_title = () => t('features_title');
export const feature_realtime_title = () => t('feature_realtime_title');
export const feature_realtime_desc = () => t('feature_realtime_desc');
export const feature_offline_title = () => t('feature_offline_title');
export const feature_offline_desc = () => t('feature_offline_desc');
export const feature_species_title = () => t('feature_species_title');
export const feature_species_desc = () => t('feature_species_desc');
export const feature_multilang_title = () => t('feature_multilang_title');
export const feature_multilang_desc = () => t('feature_multilang_desc');
export const feature_lowresource_title = () => t('feature_lowresource_title');
export const feature_lowresource_desc = () => t('feature_lowresource_desc');
export const feature_webui_title = () => t('feature_webui_title');
export const feature_webui_desc = () => t('feature_webui_desc');
export const installation_title = () => t('installation_title');
export const installation_desc = () => t('installation_desc');
export const installation_command = () => t('installation_command');
export const platforms_title = () => t('platforms_title');
export const footer_license = () => t('footer_license');
export const footer_community = () => t('footer_community');
export const nav_home = () => t('nav_home');
export const nav_features = () => t('nav_features');
export const nav_installation = () => t('nav_installation');
export const nav_docs = () => t('nav_docs');
