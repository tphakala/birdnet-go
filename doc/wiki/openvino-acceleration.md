# OpenVINO Acceleration Guide

OpenVINO is an optional inference backend that can run BirdNET-Go's neural network models on supported hardware instead of the default ONNX Runtime path. Its two main uses are:

- **Intel iGPU / dGPU offload** on x86-64 (amd64) hosts: move BirdNET v2.4 and Perch v2 inference onto an Intel integrated GPU, freeing the CPU. This is the headline use case for people running multiple RTSP streams on Intel N100/N305/Core machines.
- **The OpenVINO CPU device** on ARMv8.2+ CPUs (the Cortex-A76 in the Raspberry Pi 5), where OpenVINO can run models at f16. It is opt-in: with the default `birdnet.backend: auto` and `birdnet.openvinodevice: auto`, `auto` keeps arm64 on ONNX Runtime while it is installed, which was close to or faster than OpenVINO and used less memory in measurements on a Pi 4 and a Pi 5. BirdNET v2.4 runs at f32 there (see the table below).

> **OpenVINO is opt-in and safe.** The backend is loaded at runtime and self-gates: if it is not built in, the runtime libraries are missing, or no supported device is present, BirdNET-Go silently falls back to ONNX Runtime. You never have to enable it, and enabling it never breaks a working install. To keep every model on ONNX Runtime, set `birdnet.backend: onnx`.

## Do You Need This Guide?

**You want this if:** you run on an Intel machine with an integrated GPU and want to offload inference to it, or you run a Raspberry Pi 5 and want to opt into the OpenVINO CPU path with `birdnet.backend: openvino` or `birdnet.openvinodevice: cpu`.

**You can skip this if:** you are on a Raspberry Pi 4 or older ARM board (no native f16, OpenVINO is slower there), on Windows or macOS (the OpenVINO backend is Linux-only), or you are happy with ONNX Runtime performance.

**Docker / container installs:** The published Linux images bundle the OpenVINO runtime libraries. amd64 images also bundle the Intel GPU compute runtime (NEO/OpenCL driver), including Intel's legacy driver track for older Gen8, Gen9 and Gen11 iGPUs (for example Coffee Lake UHD 630), so iGPU offload works as soon as you pass the device through. See [Enabling OpenVINO in Docker](#enabling-openvino-in-docker).

**Release tarballs / native binaries:** The Linux binaries are built with the OpenVINO backend compiled in, **but the OpenVINO runtime libraries are not included in the tarball** (only `libonnxruntime.so` and `libtensorflowlite_c.so` are). You install the OpenVINO runtime yourself. For iGPU offload you also install the Intel GPU driver on the host. See [Enabling OpenVINO on Native / Binary Installs](#enabling-openvino-on-native--binary-installs).

## What OpenVINO Accelerates

OpenVINO is applied per model, only where it is known to be correct. Everything else keeps running on ONNX Runtime.

| Model                                      | OpenVINO eligible | GPU precision | Notes                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                 |
| ------------------------------------------ | :---------------: | :-----------: | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| BirdNET v2.4 (stock classifier)            |        Yes        |      f32      | Forced to f32 on every device. The GPU f16 kernel miscompiles this model (f32 on the iGPU is still faster than ORT CPU), and on the ARM A76 CPU f16 drops the scores of windows dominated by low-frequency noise such as wind to about zero. On arm64, `auto` with the default device runs it on ONNX Runtime (unless ONNX Runtime is missing); OpenVINO at f32 was slower or tied and used 114 to 166 MB more memory on a Pi 4 and a Pi 5. INT8 builds follow the INT8 row.                                                                                                                                                                          |
| Perch v2 (`no_dft` variant)                |        Yes        |      f32      | Forced to f32 on the GPU: the GPU f16 kernel returns NaN logits on Intel Arc. On the ARM A76 CPU it runs at f16, only when you opt in (`auto` keeps arm64 on ONNX Runtime). The stock `perch_v2.onnx` (with the DFT layer) is **not** OpenVINO-eligible; the `no_dft` variant is.                                                                                                                                                                                                                                                                                                                                                                     |
| BattyBirdNET (bat embedding)               |        Yes        |      f32      | Forced to f32 on every device (its embedding head overflows at f16).                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                  |
| INT8 BirdNET v2.4 (e.g. the arm64 default) | Only when forced  | f32 (forced)  | On `auto`, INT8 runs on ONNX Runtime CPU on every device: INT8 weights are not validated on OpenVINO, and ONNX Runtime runs them faster than OpenVINO at f32 on a Pi 5. Setting `birdnet.backend: openvino` runs it on OpenVINO at f32 (correct, but slower than ONNX Runtime on a Pi 5). The same applies to a BirdNET v2.4 model file whose name carries no precision token (`fp32`, `fp16` or `int8`), or more than one, since its precision cannot be verified; to keep OpenVINO on `auto`, name the file with exactly one precision token that matches the model's actual precision (`fp32` or `fp16`). INT8 Perch builds are ONNX Runtime only. |
| Custom / other models                      |        No         |       -       | Fall back to ONNX Runtime.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                            |

## How the Backend and Device Are Chosen

Two settings control OpenVINO. Both default to `auto`, which is almost always the right choice.

| Setting                  | Values                               | Meaning                                                                                                                                                                                                                                                    |
| ------------------------ | ------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `birdnet.backend`        | `auto` (default), `onnx`, `openvino` | `auto` tries OpenVINO where eligible (the Intel GPU; on arm64 with the default device, only when ONNX Runtime is unavailable) and falls back to ORT. `openvino` tries OpenVINO where eligible, including the arm64 CPU. `onnx` disables OpenVINO entirely. |
| `birdnet.openvinodevice` | `auto` (default), `cpu`, `gpu`       | Which OpenVINO device to target.                                                                                                                                                                                                                           |

Device resolution:

- **`gpu`** - use an Intel GPU (iGPU/dGPU via the Intel GPU plugin). Requires the OpenCL/compute driver and `/dev/dri`. If no GPU is available, OpenVINO is declined and inference falls back to ORT.
- **`cpu`** - use the OpenVINO CPU device. It is supported only on ARMv8.2+/A76 (native f16); on a 4 GB Pi 5 it was no faster than ORT in measurements and used more memory. On amd64 an explicit `cpu` is allowed (for benchmarking) but is not selected by `auto`, because the x86 CPU path has no f16 advantage over ORT. Setting `cpu` explicitly also opts an arm64 host into the OpenVINO CPU under `birdnet.backend: auto` for models with eligible weights (INT8 or unrecognized weights additionally need `birdnet.backend: openvino`).
- **`auto`** (default) - prefer the GPU when one is available, otherwise use the CPU device when it is supported (A76) and you opted in with `birdnet.backend: openvino`, or ONNX Runtime is missing (BirdNET v2.4 only); otherwise fall back to ORT. On arm64 with `birdnet.backend: auto`, this setting does not load OpenVINO while ONNX Runtime is usable.

In practice:

- **amd64 + Intel iGPU** → `auto`/`auto` offloads BirdNET v2.4 and Perch v2 to the iGPU.
- **Raspberry Pi 5 (A76)** → `auto`/`auto` runs every ONNX model on ONNX Runtime and does not load OpenVINO while ONNX Runtime is usable (there is no GPU); a native install that still uses the embedded TFLite model stays on TFLite, so select an eligible ONNX model first. To use the OpenVINO CPU (f32 for BirdNET v2.4, BirdNET v3.0 and the bat model, f16 for Perch v2), set `birdnet.backend: openvino`; `birdnet.openvinodevice: cpu` alone also works for models with eligible weights, while INT8 or unrecognized weights additionally need `birdnet.backend: openvino`. Without ONNX Runtime, an FP32 BirdNET v2.4 build still runs on the OpenVINO CPU on `auto`.
- **Anything else** → OpenVINO is declined and ORT is used.

## Enabling OpenVINO in Docker

The Linux images already contain the OpenVINO runtime. For iGPU offload you only need to pass the render device through and set the config; the amd64 image ships the Intel GPU driver (current and legacy tracks, covering older Gen8, Gen9 and Gen11 iGPUs as well as newer ones) and the entrypoint grants the runtime user access to the device automatically.

> **Image version:** the bundled Intel GPU driver landed recently, so use a current image (`nightly`, or a stable release from after the feature shipped) for iGPU offload. OpenVINO CPU acceleration works on older images too.

### CPU (no GPU)

There is nothing to install. If you are on an amd64 host without an iGPU you will get no benefit; on a Pi 5 `auto` with the default device keeps every ONNX model on ONNX Runtime while it is installed (BirdNET v2.4 uses the OpenVINO CPU only when ONNX Runtime is missing), which was close to or faster than OpenVINO and used less memory in measurements. To use the OpenVINO CPU anyway, set in your mounted `config.yaml` (an INT8 model then runs at f32):

```yaml
birdnet:
  backend: openvino # auto keeps arm64 on ONNX Runtime
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

   This example uses bridge networking. With host networking (recommended for new installs), replace `-p 8080:8080` with `--network host` and set the port with `--env BIRDNET_WEBSERVER_PORT=<port>`; see [Manual Docker Installation](installation.md#manual-docker-installation-advanced-linux-only). The device options are the same in both modes.

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

   Both premade Compose files (`docker-compose.host.yml` and `docker-compose.yml`) already contain a commented-out `/dev/dri:/dev/dri` line under `devices:`; uncomment it there.

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

For arm64 (Raspberry Pi 5), replace `x86_64` with `arm64` and `intel64` with `aarch64` in the paths above (there is no GPU plugin on arm64), and opt in with `birdnet.backend: openvino` or `birdnet.openvinodevice: cpu`, since `auto` with the default device keeps arm64 on ONNX Runtime while it is installed.

If you cannot install to a system path, or want to pin an exact library, set `birdnet.openvinopath` to the full path of `libopenvino_c.so` in your `config.yaml`, and make sure the other OpenVINO `.so` files are reachable (e.g. via `LD_LIBRARY_PATH` or Intel's `setupvars.sh`).

### Step 2 (iGPU only): Install the Intel GPU compute runtime

For iGPU offload the host needs Intel's NEO/OpenCL compute runtime and the Level-Zero driver. Without it, OpenVINO's GPU plugin cannot initialize a device and OpenVINO falls back to the CPU/ORT path.

Option A - distribution package (simplest, may be older):

```bash
sudo apt-get install -y intel-opencl-icd
```

Option B - Intel's NEO release packages (newer, matches the Docker image). Current releases no longer support Gen8, Gen9 and Gen11 iGPUs; for those, use Intel's legacy package track described in [LEGACY_PLATFORMS.md](https://github.com/intel/compute-runtime/blob/master/LEGACY_PLATFORMS.md). Download the `intel-opencl-icd`, `intel-igc-core-2`, `intel-igc-opencl-2`, `libigdgmm12`, and `libze-intel-gpu1` `.deb` packages from the [Intel compute-runtime releases](https://github.com/intel/compute-runtime/releases) and the [intel-graphics-compiler releases](https://github.com/intel/intel-graphics-compiler/releases), then `sudo dpkg -i *.deb`. The exact versions BirdNET-Go's amd64 image ships are pinned in the project `Dockerfile` (`NEO_VERSION`, `IGC_VERSION`, `GMMLIB_VERSION`).

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
  backend: auto # auto or openvino (on arm64, openvino or openvinodevice: cpu is needed for the OpenVINO CPU)
  openvinodevice: auto # auto, gpu for the iGPU, or cpu
  # openvinopath: /usr/local/lib/libopenvino_c.so   # only if not on the loader path
```

Start BirdNET-Go and [verify](#verifying-openvino-is-active).

## Verifying OpenVINO Is Active

Do not assume it worked; confirm it. There are four independent ways.

### 1. The inference status API

Replace `8080` with your web port if you changed it (for example `WEB_PORT` with host networking).

```bash
curl -s http://localhost:8080/api/v2/system/inference | jq '.backends.openvino, (.models[] | {name, backend, device})'
```

- `backends.openvino.supported: true` means the binary has the OpenVINO backend built in.
- `backends.openvino.devices` lists the OpenVINO devices detected. `["CPU","GPU"]` means the iGPU is visible; if `GPU` is missing, the driver/device passthrough is not working. On arm64 with `birdnet.backend: auto`, `birdnet.openvinodevice: auto` and ONNX Runtime installed, OpenVINO is not loaded, so this list is empty. With `birdnet.openvinodevice: gpu` OpenVINO is loaded to probe the GPU, and once loaded it stays loaded until the service restarts, so do not infer the library state from these settings alone.
- `backends.openvino.active: true` means at least one model is currently running on OpenVINO.
- Each model reports a live `device` (`CPU` or `GPU`) and `backend` (`OpenVINO` when running there).

### 2. The Web UI

Open the **System** page and find the **AI Models & Inference** section. It shows the detected backends, the OpenVINO devices, and, per loaded model, which device and backend it is actually running on.

### 3. Startup logs

BirdNET-Go logs the device planning decision at startup. When OpenVINO is declined it logs the exact reason (for example `gpu device requested but not available`, `no supported OpenVINO device`, or `ONNX Runtime is preferred on arm64 CPUs`), which tells you what is missing rather than failing silently.

### 4. Watch the GPU (iGPU only)

On the host, `intel_gpu_top` (from `intel-gpu-tools`) shows the render/compute engine busy while inference runs:

```bash
sudo apt-get install -y intel-gpu-tools
sudo intel_gpu_top
```

With Perch v2 or BirdNET v2.4 on the iGPU you will see the **Render/3D** (or **Compute**) engine ramp up and the GPU frequency rise during detections.

## Performance Expectations

- **amd64 + Intel iGPU:** offloading FP32 models to the iGPU is faster than CPU inference and, more importantly, moves the load off the CPU. On a 12th-gen Core with an Iris Xe iGPU, Perch v2 ran around 45 ms on the iGPU (measured at f16, before the f32 override; expect somewhat more at f32) versus about 67 ms on four CPU threads, while leaving those CPU cores free for other streams. On an Intel Arc A380, Perch v2 at f32 runs around 19 ms.
- **Raspberry Pi 5 (A76):** on arm64, `auto` with the default device uses ONNX Runtime for every ONNX model (BirdNET v2.4 uses the OpenVINO CPU only when ONNX Runtime is missing). BirdNET v2.4 measurements on a Pi 4 and a Pi 5 (ONNX Runtime 1.31.1, OpenVINO 2026.2.1): OpenVINO at f32 was 0.96x to 1.38x the ONNX Runtime latency and held 114 to 166 MB more memory per model. OpenVINO f16 on the A76 changed the top-1 species on some windows, so BirdNET v2.4 never runs at f16.
- **Raspberry Pi 4 and older ARM (no native f16):** OpenVINO is slower, uses more memory, and loads more slowly than ONNX Runtime. Do not enable it; keep the default `auto` (ONNX Runtime).

Because everything self-gates to ORT, the safe way to find out is to set `birdnet.backend: openvino` (on arm64) or keep `auto` (Intel iGPU), restart, and check the inference status API for whether models actually landed on OpenVINO and whether latency improved.

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

Check the model is OpenVINO-eligible (see [What OpenVINO Accelerates](#what-openvino-accelerates)). INT8 BirdNET v2.4 models (on `auto`), BirdNET v2.4 model files whose name carries no precision token or more than one (on `auto`), INT8 Perch builds, the stock `perch_v2.onnx` with the DFT layer, and custom models stay on ONNX Runtime by design, as does any model on arm64 with `birdnet.backend: auto` while ONNX Runtime is installed, unless `birdnet.openvinodevice` is `cpu` for models with eligible weights (INT8 or unrecognized weights additionally need `birdnet.backend: openvino`; a native install using the embedded TFLite model stays on TFLite). Also confirm `birdnet.backend` is not set to `onnx`.

### Detections look wrong after forcing GPU f16

Do not override precision manually. BirdNET v2.4 is intentionally run at f32 on every device because f16 breaks it on both the iGPU and the ARM A76 CPU; the bat embedding model and BirdNET v3.0 are f32 on every device as well. `auto` handles this for you.

## Platform Support Matrix

| Platform | Architecture    | OpenVINO backend in binary |     iGPU offload      |                      CPU acceleration                      |
| -------- | --------------- | :------------------------: | :-------------------: | :--------------------------------------------------------: |
| Linux    | x86-64 (amd64)  |            Yes             | Yes (Intel iGPU/dGPU) |                Benchmark only (no f16 win)                 |
| Linux    | aarch64 (arm64) |            Yes             |           -           | Opt-in: `backend: openvino` or device `cpu`; ORT on `auto` |
| Windows  | x86-64          |             -              |           -           |                             -                              |
| macOS    | any             |             -              |           -           |                             -                              |

## Further Reading

- [ONNX Runtime Installation](onnx-runtime-installation.md) - the default backend and its library setup
- [Configuration Reference](configuration-reference.md) - `birdnet.backend`, `birdnet.openvinodevice`, `birdnet.openvinopath`
- [Detection Pipeline Architecture](detection-pipeline.md) - how models are loaded and audio is routed
- [OpenVINO toolkit](https://storage.openvinotoolkit.org/repositories/openvino/packages/) - official runtime archives
- [Intel compute-runtime (NEO)](https://github.com/intel/compute-runtime/releases) - Intel GPU OpenCL/Level-Zero driver
