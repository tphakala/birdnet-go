import { describe, it, expect } from 'vitest';
import { modelLabel } from './modelLabel';

describe('modelLabel', () => {
  it('appends the live backend when one is known', () => {
    expect(modelLabel({ name: 'BirdNET v2.4', backend: 'ONNX' })).toBe('BirdNET v2.4 (ONNX)');
  });

  it('returns the bare name when no backend is known', () => {
    expect(modelLabel({ name: 'BirdNET v2.4' })).toBe('BirdNET v2.4');
    expect(modelLabel({ name: 'BirdNET v2.4', backend: '' })).toBe('BirdNET v2.4');
  });
});
