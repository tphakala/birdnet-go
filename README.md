# BirdNET-Go

<p align="center">
  <img src="doc/go-birdnet-logo.webp" />
</p>
<p align="center">
<a href="https://creativecommons.org/licenses/by-nc-sa/4.0/">
 <img src="https://badgen.net/badge/License/CC-BY-NC-SA%204.0/green">
 </a>
 <img src="https://badgen.net/badge/OS/Linux%2C%20Windows%2C%20macOS/blue">
</p>

BirdNET-Go is an application inspired by BirdNET Analyzer, developed by the K. Lisa Yang Center for Conservation Bioacoustics at the Cornell Lab of Ornithology and Chemnitz University of Technology. While the original BirdNET is based on Python, BirdNET-Go is built using Golang, aiming for simplified deployment across multiple platforms, from Windows PCs to single board computers like Raspberry Pi.

BirdNET-Go features

- Works on Windows, Linux and macOS
- Can analyse 48kHz WAV files from single file or directory of WAV files
- Outputs to Raven selection table and CSV format
- Realtime analysis of soundcard capture, analysis output to log file, SQLite or MySQL
- Log file output can be used as overlay in OBS for bird feeder streams etc.
- Localized species labels thanks to translations by @patlevin
- Minimal runtime dependencies, BirdNET Tensorflow Lite model is embedded in compiled binary

## Executable Distributions

Ready to run binaries can be found from releases section https://github.com/tphakala/BirdNET-Go/releases/
Arhives also contains libtensorflowlite_c library.

## Compiling for Linux

### Install TensorFlow Lite C library and setup headers for compile process

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

## Compiling for Windows

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

## Usage

```bash
BirdNET-Go CLI

Usage:
  birdnet [command]

Available Commands:
  authors     Print the list of authors
  completion  Generate the autocompletion script for the specified shell
  directory   Analyze all *.wav files in a directory
  file        Analyze an audio file
  help        Help about any command
  license     Print the license of Go-BirdNET
  realtime    Analyze audio in realtime mode

Flags:
      --debug               Enable debug output
  -h, --help                help for birdnet
      --locale string       Set the locale for labels. Accepts full name or 2-letter code. (default "finnish")
      --overlap float       Overlap value between 0.0 and 2.9
      --sensitivity float   Sigmoid sensitivity value between 0.0 and 1.5 (default 1)
      --threshold float     Confidency threshold for detections, value between 0.1 to 1.0 (default 0.8)

Use "birdnet [command] --help" for more information about a command.
```

## License

Creative Commons Attribution-NonCommercial-ShareAlike 4.0 International

## Authors

Tomi P. Hakala

BirdNET model by the K. Lisa Yang Center for Conservation Bioacoustics at the Cornell Lab of Ornithology in collaboration with Chemnitz University of Technology. Stefan Kahl, Connor Wood, Maximilian Eibl, Holger Klinck.

BirdNET label translations by Patrick Levin for BirdNET-Pi project by Patrick McGuire.
