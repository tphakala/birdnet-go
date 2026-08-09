# OpenVINO Acceleration Guide

OpenVINO is an optional inference backend that can run BirdNET-Go's neural network models faster than the default ONNX Runtime path on supported hardware. Its two main uses are:

- **Intel iGPU / dGPU offload** on x86-64 (amd64) hosts: move BirdNET v2.4 and Perch v2 inference onto an Intel integrated GPU, freeing the CPU. This is the headline use case for people running multiple RTSP streams on Intel N100/N305/Core machines.
- **Native FP16 CPU acceleration** on ARMv8.2+ CPUs (the Cortex-A76 in the Raspberry Pi 5), where the OpenVINO f16 CPU path can beat ONNX Runtime.

> **OpenVINO is opt-in and safe.** The backend is loaded at runtime and self-gates: if it is not built in, the runtime libraries are missing, or no supported device is present, BirdNET-Go silently falls back to ONNX Runtime. You never have to enable it, and enabling it never breaks a working install. If OpenVINO gives no benefit on your hardware, leave it on `auto` (the default) and nothing changes.

## Do You Need This Guide?

**You want this if:** you run on an Intel machine with an integrated GPU and want to offload inference to it, or you run a Raspberry Pi 5 and want to try the f16 CPU path.

**You can skip this if:** you are on a Raspberry Pi 4 or older ARM board (no native f16, OpenVINO is slower there), on Windows or macOS (the OpenVINO backend is Linux-only), or you are happy with ONNX Runtime performance.

**Docker / container installs:** The published Linux images bundle the OpenVINO runtime libraries. amd64 images also bundle the Intel GPU compute runtime (NEO/OpenCL driver), so iGPU offload works as soon as you pass the device through. See [Enabling OpenVINO in Docker](#enabling-openvino-in-docker).

**Release tarballs / native binaries:** The Linux binaries are built with the OpenVINO backend compiled in, **but the OpenVINO runtime libraries are not included in the tarball** (only `libonnxruntime.so` and `libtensorflowlite_c.so` are). You install the OpenVINO runtime yourself. For iGPU offload you also install the Intel GPU driver on the host. See [Enabling OpenVINO on Native / Binary Installs](#enabling-openvino-on-native--binary-installs).

## What OpenVINO Accelerates

OpenVINO is applied per model, only where it is known to be correct and faster. Everything else keeps running on ONNX Runtime.

| Model                                         | OpenVINO eligible | GPU precision | Notes                                                                                                                                   |
| --------------------------------------------- | :---------------: | :-----------: | --------------------------------------------------------------------------------------------------------------------------------------- |
| BirdNET v2.4 (stock classifier)               |        Yes        |      f32      | The GPU f16 kernel miscompiles this model, so it is forced to f32 on the iGPU (still faster than ORT CPU). f16 on the ARM A76 CPU path. |
| Perch v2 (`no_dft` variant)                   |        Yes        |      f16      | Runs its own OpenVINO path. The stock `perch_v2.onnx` (with the DFT layer) is **not** OpenVINO-eligible; the `no_dft` variant is.       |
| BattyBirdNET (bat embedding)                  |        Yes        |      f32      | Forced to f32 on every device (its embedding head overflows at f16).                                                                    |
| INT8 models (e.g. the arm64 INT8-ARM default) |        No         |       -       | INT8 stays on ONNX Runtime CPU.                                                                                                         |
| Custom / other models                         |        No         |       -       | Fall back to ONNX Runtime.                                                                                                              |

## How the Backend and Device Are Chosen

Two settings control OpenVINO. Both default to `auto`, which is almost always the right choice.

| Setting                  | Values                               | Meaning                                                                                                    |
| ------------------------ | ------------------------------------ | ---------------------------------------------------------------------------------------------------------- |
| `birdnet.backend`        | `auto` (default), `onnx`, `openvino` | `auto` and `openvino` try OpenVINO where eligible and fall back to ORT. `onnx` disables OpenVINO entirely. |
| `birdnet.openvinodevice` | `auto` (default), `cpu`, `gpu`       | Which OpenVINO device to target.                                                                           |

Device resolution:

- **`gpu`** - use an Intel GPU (iGPU/dGPU via the Intel GPU plugin). Requires the OpenCL/compute driver and `/dev/dri`. If no GPU is available, OpenVINO is declined and inference falls back to ORT.
- **`cpu`** - use the OpenVINO CPU device. This is a genuine win only on ARMv8.2+/A76 (native f16). On amd64 an explicit `cpu` is allowed (for benchmarking) but is not selected by `auto`, because the x86 CPU path has no f16 advantage over ORT.
- **`auto`** (default) - prefer the GPU when one is available, otherwise use the CPU device when it is a supported win (A76), otherwise fall back to ORT.

In practice:

- **amd64 + Intel iGPU** → `auto`/`auto` offloads BirdNET v2.4 and Perch v2 to the iGPU.
- **Raspberry Pi 5 (A76)** → `auto`/`auto` uses the OpenVINO f16 CPU path (there is no GPU).
- **Anything else** → OpenVINO is declined and ORT is used.

## Enabling OpenVINO in Docker

The Linux images already contain the OpenVINO runtime. For iGPU offload you only need to pass the render device through and set the config; the amd64 image ships the Intel GPU driver and the entrypoint grants the runtime user access to the device automatically.

> **Image version:** the bundled Intel GPU driver landed recently, so use a current image (`nightly`, or a stable release from after the feature shipped) for iGPU offload. OpenVINO CPU acceleration works on older images too.

### CPU (no GPU)

There is nothing to install. If you are on an amd64 host without an iGPU you will get no benefit; on a Pi 5 the f16 CPU path is used automatically on `auto`. To force it, set in your mounted `config.yaml`:

```yaml
birdnet:
  backend: auto # or openvino
  openvinodevice: auto # cpu to force the OpenVINO CPU device
```

### Intel iGPU offload

1. Pass the render device into the container. With `docker run`:

   ```bash
   docker run -d --name birdnet-go \
     -p 8080:8080 \
     -v ./config:/config -v ./data:/data \
     --device /dev/snd \
     --device /dev/dri/renderD128 \
     ghcr.io/tphakala/birdnet-go:nightly
   ```

   Docker Compose:

   ```yaml
   services:
     birdnet-go:
       image: ghcr.io/tphakala/birdnet-go:nightly
       devices:
         - /dev/snd
         - /dev/dri/renderD128
       # ... your other settings
   ```

   If you have more than one render node, pass the specific one for your Intel GPU (usually `renderD128`). Passing the whole `/dev/dri` directory also works.

2. Enable OpenVINO GPU in the mounted `config.yaml`:

   ```yaml
   birdnet:
     backend: auto # auto or openvino
     openvinodevice: auto # auto or gpu
   ```

3. Restart the container and [verify](#verifying-openvino-is-active).

The entrypoint detects the render device's group, creates a matching group inside the container, and adds the BirdNET-Go runtime user to it, so the non-root process can reach the iGPU. You will see a line like `Added birdnet to group drm-render (GID 992) for Intel iGPU access` in the container log.

**Rootless Podman note:** with rootless Podman the container user must retain access to the host render group. Add `--group-add keep-groups` (and pass `--device /dev/dri/renderD128`). Rootful Podman and Docker behave like the example above.

## Enabling OpenVINO on Native / Binary Installs

This covers running the release binary (or your own build) directly on the host, including LXC/Proxmox containers and systemd services. Two pieces are needed that the tarball does not ship: the **OpenVINO runtime libraries** (always), and the **Intel GPU driver** (only for iGPU offload).

### Step 1: Install the OpenVINO runtime libraries

Download the OpenVINO toolkit archive that matches the version BirdNET-Go is built against. The current version is defined by `OPENVINO_RELEASE` / `OPENVINO_BUILD` in the project `Taskfile.yml` (at time of writing, release `2026.2`). Keeping the runtime on the same release the binary was compiled against avoids C-API mismatches.

Linux x86-64 (amd64):

```bash
OV_RELEASE=2026.2
OV_BUILD=2026.2.0.21903.52ddc073857     # match Taskfile OPENVINO_BUILD
OV_BASE="openvino_toolkit_ubuntu22_${OV_BUILD}_x86_64"

curl -fsSL "https://storage.openvinotoolkit.org/repositories/openvino/packages/${OV_RELEASE}/linux/${OV_BASE}.tgz" \
  -o openvino.tgz
mkdir -p ov-tmp && tar xzf openvino.tgz -C ov-tmp --strip-components=1

# Install the runtime libraries to a system library path. The libopenvino*.so*
# glob covers the core, C API, the ONNX/IR frontends, and the CPU + GPU plugins;
# TBB is copied separately.
sudo cp -a ov-tmp/runtime/lib/intel64/libopenvino*.so* /usr/local/lib/
sudo cp -a ov-tmp/runtime/3rdparty/tbb/lib/libtbb*.so* /usr/local/lib/
sudo ldconfig
rm -rf openvino.tgz ov-tmp

# Verify the loader can find the C API library
ldconfig -p | grep libopenvino_c
```

For arm64 (Raspberry Pi 5), replace `x86_64` with `arm64` and `intel64` with `aarch64` in the paths above (there is no GPU plugin on arm64).

If you cannot install to a system path, or want to pin an exact library, set `birdnet.openvinopath` to the full path of `libopenvino_c.so` in your `config.yaml`, and make sure the other OpenVINO `.so` files are reachable (e.g. via `LD_LIBRARY_PATH` or Intel's `setupvars.sh`).

### Step 2 (iGPU only): Install the Intel GPU compute runtime

For iGPU offload the host needs Intel's NEO/OpenCL compute runtime and the Level-Zero driver. Without it, OpenVINO's GPU plugin cannot initialize a device and OpenVINO falls back to the CPU/ORT path.

Option A - distribution package (simplest, may be older):

```bash
sudo apt-get install -y intel-opencl-icd
```

Option B - Intel's NEO release packages (newer, matches the Docker image). Download the `intel-opencl-icd`, `intel-igc-core-2`, `intel-igc-opencl-2`, `libigdgmm12`, and `libze-intel-gpu1` `.deb` packages from the [Intel compute-runtime releases](https://github.com/intel/compute-runtime/releases) and the [intel-graphics-compiler releases](https://github.com/intel/intel-graphics-compiler/releases), then `sudo dpkg -i *.deb`. The exact versions BirdNET-Go's amd64 image ships are pinned in the project `Dockerfile` (`NEO_VERSION`, `IGC_VERSION`, `GMMLIB_VERSION`).

Then give the BirdNET-Go user access to the render device:

```bash
# Confirm the render node and its group
ls -l /dev/dri/renderD128           # e.g. crw-rw---- root render ...

# Add the user that runs birdnet-go to the render group
sudo usermod -aG render "$USER"     # log out/in, or restart the service
```

For a systemd service, make sure the service user is in the `render` group (and `SupplementaryGroups=render` in the unit if you set groups explicitly).

### Step 3: Configure and run

```yaml
birdnet:
  backend: auto # auto or openvino
  openvinodevice: auto # auto or gpu for the iGPU
  # openvinopath: /usr/local/lib/libopenvino_c.so   # only if not on the loader path
```

Start BirdNET-Go and [verify](#verifying-openvino-is-active).

## Verifying OpenVINO Is Active

Do not assume it worked; confirm it. There are four independent ways.

### 1. The inference status API

```bash
curl -s http://localhost:8080/api/v2/system/inference | jq '.backends.openvino, (.models[] | {name, backend, device})'
```

- `backends.openvino.supported: true` means the binary has the OpenVINO backend built in.
- `backends.openvino.devices` lists the OpenVINO devices detected. `["CPU","GPU"]` means the iGPU is visible; if `GPU` is missing, the driver/device passthrough is not working.
- `backends.openvino.active: true` means at least one model is currently running on OpenVINO.
- Each model reports a live `device` (`CPU` or `GPU`) and `backend` (`OpenVINO` when running there).

### 2. The Web UI

Open the **System** page and find the **AI Models & Inference** section. It shows the detected backends, the OpenVINO devices, and, per loaded model, which device and backend it is actually running on.

### 3. Startup logs

BirdNET-Go logs the device planning decision at startup. When OpenVINO is declined it logs the exact reason (for example `gpu device requested but not available`, or `no supported OpenVINO device`), which tells you what is missing rather than failing silently.

### 4. Watch the GPU (iGPU only)

On the host, `intel_gpu_top` (from `intel-gpu-tools`) shows the render/compute engine busy while inference runs:

```bash
sudo apt-get install -y intel-gpu-tools
sudo intel_gpu_top
```

With Perch v2 or BirdNET v2.4 on the iGPU you will see the **Render/3D** (or **Compute**) engine ramp up and the GPU frequency rise during detections.

## Performance Expectations

- **amd64 + Intel iGPU:** offloading FP32 models to the iGPU is faster than CPU inference and, more importantly, moves the load off the CPU. On a 12th-gen Core with an Iris Xe iGPU, Perch v2 runs around 45 ms on the iGPU versus about 67 ms on four CPU threads, while leaving those CPU cores free for other streams.
- **Raspberry Pi 5 (A76):** the OpenVINO f16 CPU path can beat ONNX Runtime for FP32 models. Results vary by model.
- **Raspberry Pi 4 and older ARM (no native f16):** OpenVINO is slower, uses more memory, and loads more slowly than ONNX Runtime. Do not enable it; keep the default `onnx`/INT8-ARM path.

Because everything self-gates to ORT, the safe way to find out is to enable `auto`, restart, and check the inference status API for whether models actually landed on OpenVINO and whether latency improved.

## Troubleshooting

### OpenVINO devices list has no `GPU`

The Intel GPU driver is not reaching OpenVINO.

- **Docker:** confirm `--device /dev/dri/renderD128` (or `/dev/dri`) is passed, and that you are on an amd64 image (arm64 has no GPU plugin). Check the container log for the `Added ... to group ... for Intel iGPU access` line.
- **Native:** confirm the Intel compute runtime is installed (`clinfo` should list an Intel platform once `intel-opencl-icd` is present) and that the BirdNET-Go user is in the `render` group.

### `ov_core_compile_model failed ... [GPU] Context was not initialized`

The OpenVINO GPU plugin is present but there is no working OpenCL/NEO driver behind it. Install the Intel GPU compute runtime (Docker amd64 images already include it; on native installs see [Step 2](#step-2-igpu-only-install-the-intel-gpu-compute-runtime)).

### `failed to load libopenvino_c` / backend `supported: false`

The OpenVINO runtime libraries are not installed or not on the loader path.

```bash
ldconfig -p | grep libopenvino_c
```

Install the runtime ([Step 1](#step-1-install-the-openvino-runtime-libraries)), run `sudo ldconfig`, or set `birdnet.openvinopath` to the exact library path. Confirm the runtime release matches the version BirdNET-Go is built against.

### Permission denied opening `/dev/dri/renderD128`

The runtime user is not in the render group. In Docker the entrypoint handles this when the device is passed; for rootless Podman add `--group-add keep-groups`. On native installs add the service user to the `render` group and restart.

### Models still run on `CPU` / ORT with OpenVINO enabled

Check the model is OpenVINO-eligible (see [What OpenVINO Accelerates](#what-openvino-accelerates)). INT8 models, the stock `perch_v2.onnx` with the DFT layer, and custom models stay on ONNX Runtime by design. Also confirm `birdnet.backend` is not set to `onnx`.

### Detections look wrong after forcing GPU f16

Do not override precision manually. BirdNET v2.4 is intentionally run at f32 on the iGPU because its f16 GPU kernel miscompiles; the bat embedding model is f32 on every device. `auto` handles this for you.

## Platform Support Matrix

| Platform | Architecture    | OpenVINO backend in binary |     iGPU offload      |      CPU acceleration       |
| -------- | --------------- | :------------------------: | :-------------------: | :-------------------------: |
| Linux    | x86-64 (amd64)  |            Yes             | Yes (Intel iGPU/dGPU) | Benchmark only (no f16 win) |
| Linux    | aarch64 (arm64) |            Yes             |           -           | Yes on ARMv8.2+/A76 (Pi 5)  |
| Windows  | x86-64          |             -              |           -           |              -              |
| macOS    | any             |             -              |           -           |              -              |

## Further Reading

- [ONNX Runtime Installation](onnx-runtime-installation.md) - the default backend and its library setup
- [Configuration Reference](configuration-reference.md) - `birdnet.backend`, `birdnet.openvinodevice`, `birdnet.openvinopath`
- [Detection Pipeline Architecture](detection-pipeline.md) - how models are loaded and audio is routed
- [OpenVINO toolkit](https://storage.openvinotoolkit.org/repositories/openvino/packages/) - official runtime archives
- [Intel compute-runtime (NEO)](https://github.com/intel/compute-runtime/releases) - Intel GPU OpenCL/Level-Zero driver
