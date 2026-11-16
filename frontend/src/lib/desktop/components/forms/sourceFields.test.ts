/**
 * Source field helpers shared by the sound card and stream cards: field keys sit
 * under the tab prefix, a disabled equalizer without filters is stored as
 * nothing, and the save timing rules split discrete from continuous changes.
 */
import { describe, it, expect } from 'vitest';
import {
  defaultQuietHoursConfig,
  type EqualizerFilter,
  type EqualizerSettings,
} from '$lib/stores/settings';
import {
  AUDIO_SOURCES_FIELD_PREFIX,
  AUDIO_SOURCES_LIST_FIELD,
  STREAMS_FIELD_PREFIX,
  STREAMS_LIST_FIELD,
  isContinuousEqualizerChange,
  isContinuousQuietHoursChange,
  savedEqualizer,
  sourceField,
} from './sourceFields';

const FILTER: EqualizerFilter = { type: 'HighPass', frequency: 100, q: 0.7, passes: 1 };

describe('source field keys', () => {
  it('puts every key under its tab prefix and the list key beside the rows', () => {
    expect(sourceField(AUDIO_SOURCES_FIELD_PREFIX, 'r1', 'gain')).toBe('audio:sources:r1:gain');
    expect(sourceField(STREAMS_FIELD_PREFIX, 'r1', 'connection')).toBe(
      'rtsp:streams:r1:connection'
    );
    expect(AUDIO_SOURCES_LIST_FIELD.startsWith(AUDIO_SOURCES_FIELD_PREFIX)).toBe(true);
    expect(STREAMS_LIST_FIELD.startsWith(STREAMS_FIELD_PREFIX)).toBe(true);
  });
});

describe('savedEqualizer', () => {
  it('stores a disabled equalizer without filters as nothing, and any other as it is', () => {
    expect(savedEqualizer({ enabled: false, filters: [] })).toBeUndefined();
    expect(savedEqualizer({ enabled: true, filters: [] })).toEqual({ enabled: true, filters: [] });
    expect(savedEqualizer({ enabled: false, filters: [FILTER] })).toEqual({
      enabled: false,
      filters: [FILTER],
    });
  });
});

describe('isContinuousEqualizerChange', () => {
  it('is continuous for a filter value and discrete for the switch or the filter count', () => {
    const current: EqualizerSettings = { enabled: true, filters: [FILTER] };

    expect(
      isContinuousEqualizerChange(
        { enabled: true, filters: [{ ...FILTER, frequency: 200 }] },
        current
      )
    ).toBe(true);
    expect(isContinuousEqualizerChange({ enabled: false, filters: [FILTER] }, current)).toBe(false);
    expect(
      isContinuousEqualizerChange(
        { enabled: true, filters: [FILTER, { ...FILTER, frequency: 300 }] },
        current
      )
    ).toBe(false);
  });
});

describe('isContinuousQuietHoursChange', () => {
  const current = { ...defaultQuietHoursConfig };

  it('is continuous for times and offsets', () => {
    expect(isContinuousQuietHoursChange({ ...current, startTime: '21:30' }, current)).toBe(true);
    expect(isContinuousQuietHoursChange({ ...current, startOffset: 15 }, current)).toBe(true);
  });

  it('is discrete for the switch, the mode and the solar events', () => {
    expect(isContinuousQuietHoursChange({ ...current, enabled: true }, current)).toBe(false);
    expect(isContinuousQuietHoursChange({ ...current, mode: 'solar' }, current)).toBe(false);
    const otherEvent = current.startEvent === 'sunrise' ? 'sunset' : 'sunrise';
    expect(isContinuousQuietHoursChange({ ...current, startEvent: otherEvent }, current)).toBe(
      false
    );
  });
});
