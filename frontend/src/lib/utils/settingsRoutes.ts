/** Settings destinations; resolve the deployment prefix with buildAppUrl at each use. */
export const SETTINGS_ROUTES = {
  mainLocation: '/ui/settings/main?tab=location',
  analysisDetection: '/ui/settings/analysis?tab=detection',
  audio: '/ui/settings/audio',
  analysisModels: '/ui/settings/analysis?tab=models',
  securityServer: '/ui/settings/security?tab=server',
  securityReverseProxy: '/ui/settings/security?tab=server#reverse-proxy',
  securityTerminal: '/ui/settings/security?tab=terminal',
  notificationsRules: '/ui/settings/notifications?tab=rules',
} as const;

/** System page destinations; resolve the deployment prefix with buildAppUrl at each use. */
export const SYSTEM_ROUTES = {
  inference: '/ui/system/inference',
  database: '/ui/system/database',
} as const;
