import { describe, it, expect } from 'vitest';
import {
  hasSettingsChanged,
  extractSettingsSection,
  hasSectionChanged,
  hasAnySectionChanged,
} from './settingsChanges';

describe('settingsChanges', () => {
  describe('hasSettingsChanged', () => {
    it('returns false when both values are undefined', () => {
      expect(hasSettingsChanged(undefined, undefined)).toBe(false);
    });

    it('returns false when original is undefined', () => {
      expect(hasSettingsChanged(undefined, { test: 'value' })).toBe(false);
    });

    it('returns false when current is undefined', () => {
      expect(hasSettingsChanged({ test: 'value' }, undefined)).toBe(false);
    });

    it('returns false when objects are identical', () => {
      const obj = { test: 'value', nested: { prop: 123 } };
      expect(hasSettingsChanged(obj, obj)).toBe(false);
    });

    it('returns false when objects have same content', () => {
      const obj1 = { test: 'value', nested: { prop: 123 } };
      const obj2 = { test: 'value', nested: { prop: 123 } };
      expect(hasSettingsChanged(obj1, obj2)).toBe(false);
    });

    it('returns true when objects have different content', () => {
      const obj1 = { test: 'value1', nested: { prop: 123 } };
      const obj2 = { test: 'value2', nested: { prop: 123 } };
      expect(hasSettingsChanged(obj1, obj2)).toBe(true);
    });

    it('returns true when nested properties differ', () => {
      const obj1 = { test: 'value', nested: { prop: 123 } };
      const obj2 = { test: 'value', nested: { prop: 456 } };
      expect(hasSettingsChanged(obj1, obj2)).toBe(true);
    });

    it('handles primitive values', () => {
      expect(hasSettingsChanged('value1', 'value1')).toBe(false);
      expect(hasSettingsChanged('value1', 'value2')).toBe(true);
      expect(hasSettingsChanged(123, 123)).toBe(false);
      expect(hasSettingsChanged(123, 456)).toBe(true);
    });
  });

  describe('extractSettingsSection', () => {
    const testData = {
      audio: {
        source: 'default',
        export: {
          enabled: true,
          format: 'wav',
          retention: {
            policy: 'age',
            maxAge: '7d',
          },
        },
      },
      birdnet: {
        sensitivity: 1.0,
        threshold: 0.3,
      },
    };

    it('returns undefined for undefined data', () => {
      expect(extractSettingsSection(undefined, 'audio')).toBeUndefined();
    });

    it('returns undefined for non-object data', () => {
      expect(extractSettingsSection('string', 'audio')).toBeUndefined();
      expect(extractSettingsSection(123, 'audio')).toBeUndefined();
    });

    it('extracts top-level section', () => {
      const result = extractSettingsSection(testData, 'audio');
      expect(result).toEqual(testData.audio);
    });

    it('extracts nested section', () => {
      const result = extractSettingsSection(testData, 'audio.export');
      expect(result).toEqual(testData.audio.export);
    });

    it('extracts deeply nested section', () => {
      const result = extractSettingsSection(testData, 'audio.export.retention');
      expect(result).toEqual(testData.audio.export.retention);
    });

    it('returns undefined for non-existent path', () => {
      expect(extractSettingsSection(testData, 'nonexistent')).toBeUndefined();
      expect(extractSettingsSection(testData, 'audio.nonexistent')).toBeUndefined();
      expect(extractSettingsSection(testData, 'audio.export.nonexistent')).toBeUndefined();
    });

    it('handles empty path', () => {
      const result = extractSettingsSection(testData, '');
      expect(result).toEqual(testData);
    });
  });

  describe('hasSectionChanged', () => {
    const originalData = {
      audio: {
        source: 'default',
        export: { enabled: false, format: 'wav' },
      },
      birdnet: { sensitivity: 1.0 },
    };

    const modifiedData = {
      audio: {
        source: 'modified',
        export: { enabled: true, format: 'wav' },
      },
      birdnet: { sensitivity: 1.0 },
    };

    it('detects changes in top-level section', () => {
      expect(hasSectionChanged(originalData, modifiedData, 'audio')).toBe(true);
      expect(hasSectionChanged(originalData, modifiedData, 'birdnet')).toBe(false);
    });

    it('detects changes in nested section', () => {
      expect(hasSectionChanged(originalData, modifiedData, 'audio.export')).toBe(true);
    });

    it('returns false for non-existent sections', () => {
      expect(hasSectionChanged(originalData, modifiedData, 'nonexistent')).toBe(false);
    });

    it('handles undefined data', () => {
      expect(hasSectionChanged(undefined, modifiedData, 'audio')).toBe(false);
      expect(hasSectionChanged(originalData, undefined, 'audio')).toBe(false);
    });
  });

  describe('hasAnySectionChanged', () => {
    const originalData = {
      audio: { source: 'default' },
      birdnet: { sensitivity: 1.0 },
      filters: { enabled: false },
    };

    const modifiedData = {
      audio: { source: 'modified' },
      birdnet: { sensitivity: 1.0 },
      filters: { enabled: false },
    };

    it('returns true if any section has changed', () => {
      const sections = ['audio', 'birdnet', 'filters'];
      expect(hasAnySectionChanged(originalData, modifiedData, sections)).toBe(true);
    });

    it('returns false if no sections have changed', () => {
      const sections = ['birdnet', 'filters'];
      expect(hasAnySectionChanged(originalData, modifiedData, sections)).toBe(false);
    });

    it('returns false for empty sections array', () => {
      expect(hasAnySectionChanged(originalData, modifiedData, [])).toBe(false);
    });

    it('handles non-existent sections', () => {
      const sections = ['nonexistent1', 'nonexistent2'];
      expect(hasAnySectionChanged(originalData, modifiedData, sections)).toBe(false);
    });
  });

  describe('real-world audio settings scenarios', () => {
    const originalAudioSettings = {
      source: '',
      rtsp: {
        transport: 'tcp',
        urls: [],
      },
      soundLevel: {
        enabled: false,
        interval: 60,
      },
      equalizer: {
        enabled: false,
        filters: [],
      },
      export: {
        enabled: false,
        debug: false,
        path: 'clips/',
        format: 'wav',
        quality: '96k',
        retention: {
          policy: 'none',
          maxAge: '7d',
          maxUsage: '80%',
          minClips: 10,
          keepSpectrograms: false,
        },
      },
    };

    it('detects audio source change', () => {
      const modifiedSettings = {
        ...originalAudioSettings,
        source: 'hw:0,0',
      };
      expect(hasSettingsChanged(originalAudioSettings, modifiedSettings)).toBe(true);
    });

    it('detects export enabled change', () => {
      const modifiedSettings = {
        ...originalAudioSettings,
        export: {
          ...originalAudioSettings.export,
          enabled: true,
        },
      };
      expect(hasSettingsChanged(originalAudioSettings, modifiedSettings)).toBe(true);
    });

    it('detects nested retention policy change', () => {
      const modifiedSettings = {
        ...originalAudioSettings,
        export: {
          ...originalAudioSettings.export,
          retention: {
            ...originalAudioSettings.export.retention,
            policy: 'age',
          },
        },
      };
      expect(hasSettingsChanged(originalAudioSettings, modifiedSettings)).toBe(true);
    });

    it('detects RTSP URL changes', () => {
      const modifiedSettings = {
        ...originalAudioSettings,
        rtsp: {
          ...originalAudioSettings.rtsp,
          urls: [{ id: '1', url: 'rtsp://example.com/stream', name: 'Test' }],
        },
      };
      expect(hasSettingsChanged(originalAudioSettings, modifiedSettings)).toBe(true);
    });

    it('returns false when no changes made', () => {
      const identicalSettings = JSON.parse(JSON.stringify(originalAudioSettings));
      expect(hasSettingsChanged(originalAudioSettings, identicalSettings)).toBe(false);
    });
  });
});
