# Go-BirdNET

<p align="center">
  <img src="doc/go-birdnet-logo.webp" />
</p>
<p align="center">
<a href="https://creativecommons.org/licenses/by-nc-sa/4.0/">
 <img src="https://badgen.net/badge/License/CC-BY-NC-SA%204.0/green">
 </a>
 <img src="https://badgen.net/badge/OS/Linux%2C%20Windows%2C%20macOS/blue">
</p>

Go-BirdNET is an application inspired by BirdNET Analyzer, developed by the K. Lisa Yang Center for Conservation Bioacoustics at the Cornell Lab of Ornithology and Chemnitz University of Technology. While the original BirdNET is based on Python, Go-BirdNET is built using Golang, aiming for simplified deployment across multiple platforms, from Windows PCs to single board computers like Raspberry Pi.

Go-BirdNET features

- Works on Windows, Linux and macOS
- Can analyse 48kHz WAV files from single file or directory of WAV files
- Outputs to Raven selection table and CSV format
- Realtime analysis of soundcard capture, analysis output to log file, SQLite or MySQL
- Log file output can be used as overlay in OBS for bird feeder streams etc.
- Localized species labels thanks to translations by @patlevin
- Minimal runtime dependencies, BirdNET Tensorflow Lite model is embedded in compiled binary

## Executable Distributions

Ready to run binaries can be found from releases section https://github.com/tphakala/go-birdnet/releases/
Arhives also contains libtensorflow_c library.

## Compiling

### Building TensorFlow Lite C API

Build tflite with cmake

```bash
sudo apt-get install cmake
```

Clone tensorflow repository

```bash
git clone https://github.com/tensorflow/tensorflow.git tensorflow_src
```

Create cmake build directory

```bash
mkdir tflite_build
cd tflite_build
```

Run cmake

```bash
cmake ../tensorflow_src/tensorflow/lite/c
```

Build tflite, In the tflite_build directory do

```bash
cmake --build . -j
```

Copy compiled libtensorflowlite_c.so to /usr/local/lib and update dynamic linker run‐time bindings with ldconfig

```bash
sudo cp libtensorflowlite_c.so /usr/local/lib
sudo ldconfig
```

### Building Go-BirdNET

Clone go-birdnet repository

```bash
git clone https://github.com/tphakala/go-birdnet.git
```

Add CGO_CFLAGS and point it to directory you cloned tensorflow source in

```bash
export CGO_CFLAGS=-I$HOME/src/tensorflow
```

Build Go-BirdNET by make, compiled binary will be placed in go-BirdNET/bin directory

```bash
cd go-birdnet
make
```

## Usage

```bash
Go-BirdNET CLI

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

## ChangeLog

Realtime detection support is now added, launch with -realtime flag and application will start capturing audio from default audio device and log detections to detections.log.

## Derived works

Feel free to fork this codebase for your own projects, but please follow license which restricts commercial usage

## License

Creative Commons Attribution-NonCommercial-ShareAlike 4.0 International

## Authors

Tomi P. Hakala

BirdNET model by the K. Lisa Yang Center for Conservation Bioacoustics at the Cornell Lab of Ornithology in collaboration with Chemnitz University of Technology. Stefan Kahl, Connor Wood, Maximilian Eibl, Holger Klinck.

BirdNET label translations by Patrick Levin for BirdNET-Pi project by Patrick McGuire.
