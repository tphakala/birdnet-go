import { describe, it, expect } from 'vitest';
import { clipExportExtension } from './clipExport';

describe('clipExportExtension', () => {
  it('maps AAC to the m4a container and Opus to the ogg container', () => {
    expect(clipExportExtension('aac')).toBe('m4a');
    expect(clipExportExtension('opus')).toBe('ogg');
  });

  it('uses the format name for formats that are their own container', () => {
    for (const format of ['wav', 'flac', 'mp3']) {
      expect(clipExportExtension(format)).toBe(format);
    }
  });

  it('no longer knows ALAC, which the backend rejects', () => {
    expect(clipExportExtension('alac')).toBe('alac');
  });
});
