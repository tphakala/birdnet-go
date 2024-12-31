# Building

Building can be tricky because of the many system development library dependencies. 
It is recommended to follow the [building locally guide](#building-locally). 
However, there is also a [devcontainer](#devcontainer) approach with the dependencies all installed inside a container.

## Devcontainer approach

For development in VSCode, a [devcontainer](https://code.visualstudio.com/docs/remote/containers) can be used. 
Open the project in VSCode and hit `F1` or `Shift-Command-P` on Mac, type `Reopen in Container` and wait for the container to build.

The provided [`.devcontainer/devcontainer.json`](.devcontainer/devcontainer.json) contains all required dependencies and 
also mounts the source code into the container, so one can immediately start coding. 

After the container starts, a development server is started that can be reached at [localhost:8080](http://localhost:8080). 
The development server will automatically reload on any code changes.

### Notes: 

**VSCode Extensions:** Ensure Docker and [Remote Development](https://marketplace.visualstudio.com/items?itemName=ms-vscode-remote.vscode-remote-extensionpack) extensions are installed.

**Docker Engine:** Ensure the development host has a full docker engine install (not just the docker.io runtime package).  Instructions are at [Docker Engine Install On Debian](https://docs.docker.com/engine/install/debian/).


## Building locally approach

### Install prerequisites

#### Install TensorFlow Lite C library

Download precompiled TensorFlow Lite C library for Linux from https://github.com/tphakala/tflite_c/releases/tag/v2.14.0

Copy libtensorflowlite_c.so to /usr/local/lib and run ```ldconfig```

```bash
sudo cp libtensorflowlite_c.so /usr/local/lib
sudo ldconfig
```

Clone tensorflow repository, this is required for header files to be present while compiling with CGO

```bash
mkdir ~/src
cd ~/src
git clone https://github.com/tensorflow/tensorflow.git
```

Checkout TensorFlow v2.14.0 release

```bash
cd tensorflow
git checkout tags/v2.14.0
```

### Building BirdNET-Go

Clone BirdNET-Go repository

```bash
git clone https://github.com/tphakala/BirdNET-Go.git
```

Build BirdNET-Go by make, compiled binary will be placed in go-birdnet/bin directory

```bash
cd BirdNET-Go
make
```

#### Compiling for Windows

Windows build is cross compiled on Linux, for this you need MinGW-w64 on your build system

```bash
  sudo apt install mingw-w64-tools gcc-mingw-w64-x86-64 gcc-mingw-w64-i686
```

Download precompiled TensorFlow Lite C library for Windows from https://github.com/tphakala/tflite_c/releases/tag/v2.14.0

Copy **libtensorflowlite_c.dll** to /usr/x86_64-w64-mingw32/lib/

```bash
sudo cp libtensorflowlite_c.dll /usr/x86_64-w64-mingw32/lib/
```

Clone tensorflow repository, this is required for header files to be present while compiling with CGO

```bash
mkdir ~/src
cd ~/src
git clone https://github.com/tensorflow/tensorflow.git
```

### Cross compiling BirdNET-Go

Clone BirdNET-Go repository

```bash
git clone https://github.com/tphakala/BirdNET-Go.git
```

Build BirdNET-Go by running make windows

```bash
cd BirdNET-Go
make windows
```

Windows executable is in **bin/birdnet.exe**, copy this and **libtensorflowlite_c.so** to your Windows system, library file must be in PATH for birdnet.exe to run properly.

Yes it is correct that you need **libtensorflowlite_c.dll** in /usr/x86_64-w64-mingw32/lib/ for compile process, and on Windows you need **libtensorflowlite_c.so** for runtime. This sounds backwards but this is how it works.
