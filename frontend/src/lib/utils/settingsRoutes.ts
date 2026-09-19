/** Settings destinations; resolve the deployment prefix with buildAppUrl at each use. */
export const SETTINGS_ROUTES = {
  mainLocation: '/ui/settings/main?tab=location',
  analysisSettings: '/ui/settings/analysis?tab=settings',
  analysisModels: '/ui/settings/analysis?tab=models',
  securityServer: '/ui/settings/security?tab=server',
  securityTerminal: '/ui/settings/security?tab=terminal',
} as const;

/** System page destinations; resolve the deployment prefix with buildAppUrl at each use. */
export const SYSTEM_ROUTES = {
  inference: '/ui/system/inference',
} as const;
