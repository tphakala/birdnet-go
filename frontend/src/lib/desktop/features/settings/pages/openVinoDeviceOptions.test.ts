import { describe, it, expect } from 'vitest';
import type { OpenVINOBackendStatus } from '$lib/desktop/features/system/inference.types';
import { openVinoDeviceField } from './openVinoDeviceOptions';

/** Renders a key with its parameters, so a test sees which key and which values a label used. */
function translate(key: string, params?: Record<string, unknown>): string {
  return params ? `${key}${JSON.stringify(params)}` : key;
}

const KEY = 'analysis.inference.device';
const IGPU = { id: 'GPU.0', name: 'Intel(R) UHD Graphics 730 (iGPU)', type: 'integrated' };
const ARC = { id: 'GPU.1', name: 'Intel(R) Arc(TM) A750 Graphics (dGPU)', type: 'discrete' };

function status(overrides: Partial<OpenVINOBackendStatus> = {}): OpenVINOBackendStatus {
  return { supported: true, active: true, devices: ['CPU'], ...overrides };
}

function field(s: OpenVINOBackendStatus | undefined, selected = 'auto', saved = selected) {
  return openVinoDeviceField({ status: s, selected, saved }, translate);
}

const values = (f: ReturnType<typeof field>) => f.options.map(o => o.value);

describe('openVinoDeviceField', () => {
  it('offers Auto and CPU on a host without a GPU', () => {
    const f = field(status());
    expect(values(f)).toEqual(['auto', 'cpu']);
    expect(f.helpText).toBe(`${KEY}.helpText`);
  });

  it('maps a lone GPU reported as "GPU" to the gpu keyword and adds no extra option', () => {
    const f = field(
      status({
        devices: ['CPU', 'GPU'],
        gpus: [{ id: 'GPU', name: 'Intel(R) Iris(R) Xe Graphics' }],
      })
    );
    expect(values(f)).toEqual(['auto', 'cpu', 'gpu']);
    expect(f.helpText).toBe(`${KEY}.helpText`);
    expect(f.options[2].description).toBe(
      `${KEY}.options.gpuDefault{"name":"Intel(R) Iris(R) Xe Graphics"}`
    );
  });

  it('lists each GPU of a multi-GPU host by id, and says which adapter plain GPU uses', () => {
    const f = field(status({ devices: ['CPU', 'GPU'], gpus: [ARC, IGPU] }));
    expect(values(f)).toEqual(['auto', 'cpu', 'gpu', 'GPU.0', 'GPU.1']);
    expect(f.helpText).toBe(`${KEY}.helpTextMultipleGpus`);
    expect(f.options[2].description).toBe(`${KEY}.options.gpuDefault{"name":"${IGPU.name}"}`);
    const igpu = f.options.find(o => o.value === 'GPU.0');
    expect(igpu?.label).toBe(
      `${KEY}.options.gpuNamed{"name":"${IGPU.name}","type":"${KEY}.gpuTypes.integrated"}`
    );
    const arc = f.options.find(o => o.value === 'GPU.1');
    expect(arc?.label).toBe(
      `${KEY}.options.gpuNamed{"name":"${ARC.name}","type":"${KEY}.gpuTypes.discrete"}`
    );
    expect(arc?.disabled).toBeFalsy();
    // The id under each name tells two adapters of the same model apart.
    expect([igpu?.description, arc?.description]).toEqual(['GPU.0', 'GPU.1']);
  });

  it('names the default adapter by id when GPU.0 has no name', () => {
    const f = field(status({ devices: ['CPU', 'GPU'], gpus: [{ id: 'GPU.0' }, ARC] }));
    expect(f.options[2].description).toBe(`${KEY}.options.gpuDefault{"name":"GPU.0"}`);
  });

  it('gives a lone GPU without a name no description, since it is the only one', () => {
    const f = field(status({ devices: ['CPU', 'GPU'], gpus: [{ id: 'GPU' }] }));
    expect(values(f)).toEqual(['auto', 'cpu', 'gpu']);
    expect(f.options[2].description).toBeUndefined();
  });

  it('orders GPUs by numeric index, so GPU.10 follows GPU.2', () => {
    const f = field(
      status({
        devices: ['CPU', 'GPU'],
        gpus: [{ id: 'GPU.10' }, { id: 'GPU.2' }, { id: 'GPU.0' }],
      })
    );
    expect(values(f)).toEqual(['auto', 'cpu', 'gpu', 'GPU.0', 'GPU.2', 'GPU.10']);
  });

  it.each([
    [
      'an unknown type shown as it is',
      { type: 'virtual' },
      `${KEY}.options.gpuNamed{"name":"Card","type":"virtual"}`,
    ],
    ['an empty type left out', { type: '' }, 'Card'],
    ['no type left out', {}, 'Card'],
    ['a blank name replaced by the id', { name: '  ', type: '' }, 'GPU.0'],
  ])('tolerates %s', (_, extra, label) => {
    const f = field(
      status({
        devices: ['CPU', 'GPU'],
        gpus: [{ id: 'GPU.0', name: 'Card', ...extra }, { id: 'GPU.1' }],
      })
    );
    expect(f.options.find(o => o.value === 'GPU.0')?.label).toBe(label);
    // A GPU without a name falls back to its id.
    expect(f.options.find(o => o.value === 'GPU.1')?.label).toBe('GPU.1');
  });

  it('ignores reported ids that are not storable GPU.<n> values', () => {
    const f = field(
      status({
        devices: ['CPU', 'GPU'],
        gpus: [{ id: 'GPU.x' }, { id: 'NPU' }, { id: 'GPU.0' }, { id: 'GPU.1' }],
      })
    );
    expect(values(f)).toEqual(['auto', 'cpu', 'gpu', 'GPU.0', 'GPU.1']);
  });

  it('keeps a saved GPU the host does not report selectable, marked as not detected', () => {
    const multi = status({ devices: ['CPU', 'GPU'], gpus: [IGPU, ARC] });
    const f = field(multi, 'cpu', 'GPU.5');
    const kept = f.options.find(o => o.value === 'GPU.5');
    expect(kept?.label).toBe(`${KEY}.options.notDetected{"id":"GPU.5"}`);
    expect(kept?.disabled).toBeFalsy();
    // Only a selected missing GPU changes the help text; here CPU is selected.
    expect(f.helpText).toBe(`${KEY}.helpTextMultipleGpus`);
    expect(field(multi, 'GPU.5').helpText).toBe(`${KEY}.notDetectedHint`);
  });

  it.each([
    ['GPU.1', 'GPU.1'],
    ['gpu', 'GPU'],
  ])('marks a selected %s as not detected on a host whose probe found no GPU', (device, id) => {
    const f = field(status(), device);
    expect(values(f)).toEqual(['auto', 'cpu', device]);
    expect(f.options[2].label).toBe(`${KEY}.options.notDetected{"id":"${id}"}`);
    expect(f.helpText).toBe(`${KEY}.notDetectedHint`);
  });

  it.each([
    ['GPU.1', 'GPU.1'],
    ['gpu', `${KEY}.options.gpu`],
  ])(
    'gives no device verdict for %s on a build without OpenVINO, where the build is the cause',
    (device, label) => {
      const f = field({ supported: false, active: false }, device);
      expect(f.options.find(o => o.value === device)?.label).toBe(label);
      expect(f.helpText).toBe(`${KEY}.helpText`);
    }
  );

  it.each([
    ['before the status loads', undefined],
    ['before the device probe finished', status({ devices: undefined })],
    ['when the server sends no gpus field', status({ devices: ['CPU', 'GPU'] })],
  ])('keeps a saved GPU unmarked %s, since the GPUs are unknown', (_, s) => {
    const f = field(s, 'GPU.1');
    expect(values(f)).toContain('GPU.1');
    expect(f.options.find(o => o.value === 'GPU.1')?.label).toBe('GPU.1');
    expect(f.helpText).toBe(`${KEY}.helpText`);
  });

  it('offers plain GPU without an adapter name when the server sends no gpus field', () => {
    const f = field(status({ devices: ['CPU', 'GPU'] }));
    expect(values(f)).toEqual(['auto', 'cpu', 'gpu']);
    expect(f.options[2].description).toBeUndefined();
  });

  it('keeps a saved gpu offered on a host whose probe found no GPU, marked as not detected', () => {
    const savedGpu = field(status(), 'auto', 'gpu');
    expect(values(savedGpu)).toEqual(['auto', 'cpu', 'gpu']);
    expect(savedGpu.options[2].label).toBe(`${KEY}.options.notDetected{"id":"GPU"}`);
    expect(savedGpu.helpText).toBe(`${KEY}.helpText`);
  });

  it('keeps a selected GPU the host does not report when the saved device differs', () => {
    const f = field(status({ devices: ['CPU', 'GPU'], gpus: [IGPU, ARC] }), 'GPU.5', 'cpu');
    expect(values(f)).toEqual(['auto', 'cpu', 'gpu', 'GPU.0', 'GPU.1', 'GPU.5']);
    expect(f.helpText).toBe(`${KEY}.notDetectedHint`);
  });

  it('sorts kept GPUs the host does not report among the reported ones by index', () => {
    const reported = status({ devices: ['CPU', 'GPU'], gpus: [{ id: 'GPU.10' }, { id: 'GPU.1' }] });
    expect(values(field(reported, 'GPU.0', 'GPU.5'))).toEqual([
      'auto',
      'cpu',
      'gpu',
      'GPU.0',
      'GPU.1',
      'GPU.5',
      'GPU.10',
    ]);
  });
});
