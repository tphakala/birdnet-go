/**
 * Options and help text of the Analysis page's OpenVINO device select.
 *
 * birdnet.openVinoDevice stores auto, cpu, gpu (OpenVINO's default GPU: the lone "GPU" entry, or
 * GPU.0 with several) or a concrete GPU.<n>. GET /api/v2/system/inference reports the device classes (devices) and, once
 * the device probe has run, each GPU (gpus). A single-GPU host reports its GPU as "GPU", which is
 * not a storable value: it is the plain gpu option. With several GPUs each GPU.<n> gets its own
 * option. A choice the host lacks stays listed while it is selected or saved.
 */
import type { SelectOption } from '$lib/desktop/components/forms/SelectDropdown.types';
import type {
  OpenVINOBackendStatus,
  OpenVINOGPU,
} from '$lib/desktop/features/system/inference.types';
import type { TFunction } from '$lib/i18n/types';

/** Stored device keywords. */
const DEVICE_AUTO = 'auto';
const DEVICE_CPU = 'cpu';
const DEVICE_GPU = 'gpu';

/** A concrete GPU id, mirroring conf.IsOVGPUDeviceName in the backend. */
const CONCRETE_GPU_PATTERN = /^GPU\.(\d+)$/;
/** birdnet.backend values the explicit-selection rule checks. */
const BACKEND_OPENVINO = 'openvino';
const BACKEND_ONNX = 'onnx';
/** The GPU device class as OpenVINO names it: a lone GPU's id, and the name shown for gpu. */
const GPU_CLASS_ID = 'GPU';
/** The id of OpenVINO's default GPU: the lone "GPU", or GPU.0 with several. */
const DEFAULT_GPU_IDS = [GPU_CLASS_ID, 'GPU.0'];
/** DEVICE_TYPE values that have a translated word; any other value is shown as it is. */
const GPU_TYPE_INTEGRATED = 'integrated';
const GPU_TYPE_DISCRETE = 'discrete';

/** What the device select is built from. */
export interface OpenVinoDeviceFieldInput {
  /** backends.openvino of the inference status; undefined until it loads. gpuFailed marks every detected GPU as failed. */
  status: OpenVINOBackendStatus | undefined;
  /** The device in the form. */
  selected: string;
  /** The saved device. */
  saved: string;
}

/** The device select's options and help text. */
export interface OpenVinoDeviceField {
  /** Auto, CPU, GPU when offered, then each GPU.<n> by index, reported or kept. */
  options: SelectOption[];
  /** Help text for the select: what happens with a selected GPU the host lacks, or how to pick. */
  helpText: string;
}

function isConcreteGpuId(id: string): boolean {
  return CONCRETE_GPU_PATTERN.test(id);
}

function gpuIndex(id: string): number {
  return Number(CONCRETE_GPU_PATTERN.exec(id)?.[1] ?? Number.NaN);
}

function typeWord(type: string | undefined, t: TFunction): string {
  const value = type?.trim() ?? '';
  if (value === GPU_TYPE_INTEGRATED) return t('analysis.inference.device.gpuTypes.integrated');
  if (value === GPU_TYPE_DISCRETE) return t('analysis.inference.device.gpuTypes.discrete');
  return value;
}

/** The adapter name, or undefined when it is missing or blank. */
function gpuName(gpu: OpenVINOGPU | undefined): string | undefined {
  const name = gpu?.name?.trim();
  return name === '' ? undefined : name;
}

function gpuLabel(gpu: OpenVINOGPU, t: TFunction): string {
  const name = gpuName(gpu) ?? gpu.id;
  const type = typeWord(gpu.type, t);
  return type ? t('analysis.inference.device.options.gpuNamed', { name, type }) : name;
}

/** Builds the device options and help text from the inference status and the device values. */
export function openVinoDeviceField(
  { status, selected, saved }: OpenVinoDeviceFieldInput,
  t: TFunction
): OpenVinoDeviceField {
  const gpus = status?.gpus;
  const kept = new Set([selected, saved]);
  const hostHasGpu = status?.devices?.some(d => d.toLowerCase().startsWith(DEVICE_GPU)) ?? false;

  // After the GPU fault latch tripped, a detected GPU is shown as failed; an undetected one keeps
  // its own label.
  const gpuFailed = status?.gpuFailed === true;
  const failedLabel = (label: string) =>
    gpuFailed ? t('analysis.inference.device.options.failed', { label }) : label;

  const options: SelectOption[] = [
    { value: DEVICE_AUTO, label: t('analysis.inference.device.options.auto') },
    { value: DEVICE_CPU, label: t('analysis.inference.device.options.cpu') },
  ];

  // The GPUs are known once a build with OpenVINO has probed them: it filled gpus, or found no GPU
  // at all. Before that, and on a build without OpenVINO (where the build, not the GPU, is why
  // OpenVINO is not used), a kept GPU is listed without a verdict.
  const supported = status?.supported === true;
  const probedWithoutGpu = supported && (status.devices?.length ?? 0) > 0 && !hostHasGpu;
  const gpusKnown = supported && (gpus !== undefined || probedWithoutGpu);

  const concrete = (gpus ?? []).filter(g => isConcreteGpuId(g.id));
  const reportedIds = new Set(concrete.map(g => g.id));
  const unreported = [...kept].filter(id => isConcreteGpuId(id) && !reportedIds.has(id));
  // One verdict for the label and the help text of a device the host is known to lack.
  const notDetected = (id: string) =>
    id === DEVICE_GPU ? probedWithoutGpu : gpusKnown && unreported.includes(id);

  if (hostHasGpu || kept.has(DEVICE_GPU)) {
    const defaultGpu = DEFAULT_GPU_IDS.map(id => gpus?.find(g => g.id === id)).find(Boolean);
    // A nameless GPU.0 is named by its id; a nameless lone GPU needs no name, it is the only one.
    const defaultName =
      gpuName(defaultGpu) ??
      (defaultGpu && isConcreteGpuId(defaultGpu.id) ? defaultGpu.id : undefined);
    options.push({
      value: DEVICE_GPU,
      label: notDetected(DEVICE_GPU)
        ? t('analysis.inference.device.options.notDetected', { id: GPU_CLASS_ID })
        : failedLabel(t('analysis.inference.device.options.gpu')),
      description: defaultName
        ? t('analysis.inference.device.options.gpuDefault', { name: defaultName })
        : undefined,
    });
  }

  const gpuOptions: SelectOption[] = [
    ...concrete.map(g => ({
      value: g.id,
      label: failedLabel(gpuLabel(g, t)),
      description: g.id,
    })),
    ...unreported.map(id => ({
      value: id,
      label: notDetected(id) ? t('analysis.inference.device.options.notDetected', { id }) : id,
    })),
  ].sort((a, b) => gpuIndex(a.value) - gpuIndex(b.value));

  const helpText = notDetected(selected)
    ? t('analysis.inference.device.notDetectedHint')
    : concrete.length > 1
      ? t('analysis.inference.device.helpTextMultipleGpus')
      : t('analysis.inference.device.helpText');

  return { options: [...options, ...gpuOptions], helpText };
}

/**
 * Reports whether a saved backend and OpenVINO device select OpenVINO explicitly: backend openvino,
 * or a backend other than onnx with the device cpu, gpu or GPU.<n>. Mirrors the server's
 * openVINOSelectionExplicit (internal/classifier/model_openvino.go); an explicit selection never
 * falls back, while any other selection moves models off a failed GPU.
 */
export function isExplicitOpenVinoSelection(backend: string, device: string): boolean {
  if (backend === BACKEND_OPENVINO) return true;
  return (
    backend !== BACKEND_ONNX &&
    (device === DEVICE_CPU || device === DEVICE_GPU || CONCRETE_GPU_PATTERN.test(device))
  );
}
