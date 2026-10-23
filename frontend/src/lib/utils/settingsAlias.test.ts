import { describe, it, expect } from 'vitest';
import { SETTINGS_BASE_PATH, SETTINGS_DEFAULT_PATH, resolveSettingsAlias } from './settingsAlias';

const BARE = SETTINGS_BASE_PATH;
const MAIN = SETTINGS_DEFAULT_PATH;
const AUDIO = [BARE, 'audio'].join('/');

describe('resolveSettingsAlias', () => {
  it('maps the bare settings path to the default section', () => {
    expect(resolveSettingsAlias(BARE)).toBe(MAIN);
  });

  it('leaves section paths unchanged', () => {
    expect(resolveSettingsAlias(MAIN)).toBe(MAIN);
    expect(resolveSettingsAlias(AUDIO)).toBe(AUDIO);
  });

  it('leaves unrelated paths unchanged', () => {
    const dashboard = ['', 'ui', 'dashboard'].join('/');
    expect(resolveSettingsAlias(dashboard)).toBe(dashboard);
    expect(resolveSettingsAlias(`${BARE}x`)).toBe(`${BARE}x`);
  });
});
