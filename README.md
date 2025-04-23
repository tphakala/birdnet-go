# BirdNET-Go

<p align="center">
  <img src="doc/BirdNET-Go-logo.webp" />
</p>
<p align="center">
  <!-- Project Status -->
  <a href="https://github.com/tphakala/birdnet-go/releases">
    <img src="https://img.shields.io/github/v/release/tphakala/birdnet-go?include_prereleases&style=flat-square&color=blue">
  </a>
  <a href="https://creativecommons.org/licenses/by-nc-sa/4.0/">
    <img src="https://badgen.net/badge/License/CC-BY-NC-SA%204.0/green">
  </a>
  <img src="https://badgen.net/badge/OS/Linux%2C%20Windows%2C%20macOS/blue">

  <br>

  <!-- Code Quality -->
  <a href="https://golang.org">
    <img src="https://img.shields.io/badge/Built%20with-Go-teal?style=flat-square&logo=go">
  </a>
  <a href="https://goreportcard.com/report/github.com/tphakala/birdnet-go">
    <img src="https://goreportcard.com/badge/github.com/tphakala/birdnet-go?style=flat-square">
  </a>

  <br>

  <!-- Community -->
  <a href="https://github.com/tphakala/birdnet-go/network/members">
    <img src="https://img.shields.io/github/forks/tphakala/birdnet-go?style=flat-square&color=purple">
  <a href="https://github.com/tphakala/birdnet-go/graphs/contributors">
    <img src="https://img.shields.io/github/contributors/tphakala/birdnet-go?style=flat-square&color=orange">
  </a>
  </a>
  <a href="https://github.com/tphakala/birdnet-go/issues">
    <img src="https://img.shields.io/github/issues/tphakala/birdnet-go?style=flat-square&color=red">
  </a>

  <a href="https://coderabbit.ai">
    <img src="https://img.shields.io/coderabbit/prs/github/tphakala/birdnet-go?utm_source=oss&utm_medium=github&utm_campaign=tphakala%2Fbirdnet-go&labelColor=171717&color=FF570A&link=https%3A%2F%2Fcoderabbit.ai&label=CodeRabbit+Reviews">
  </a>
</p>

BirdNET-Go is an AI solution for continuous avian monitoring and identification

- 24/7 realtime bird song analysis of soundcard capture, analysis output to log file, SQLite or MySQL
- Utilizes BirdNET AI model trained with more than 6500 bird species
- Local processing, Internet connectivity not required
- Easy to use Web user interface for data visualisation
- Supports over 40 languages for species names
- Advanced features like [Deep Detection](doc/wiki/guide.md#deep-detection) for improved accuracy and [Live Audio Streaming](doc/wiki/guide.md#live-audio-streaming).
- BirdWeather.com API integration
- Realtime log file output can be used as overlay in OBS for bird feeder streams etc.
- Minimal runtime dependencies, BirdNET Tensorflow Lite model is embedded in compiled binary
- Provides endpoint for Prometheus data scraping
- Runs on Windows, Linux and macOS
- Low resource usage, works on Raspberry Pi 3 and equivalent 64-bit single board computers

## Installation

Quick install script for Debian, Ubuntu and Raspberry Pi OS based systems:

```bash
curl -fsSL https://github.com/tphakala/birdnet-go/raw/main/install.sh -o install.sh
bash ./install.sh
```

## Help Improve BirdNET-Go

I am seeking web developer with experience in Go HTML templating, Tailwind CSS, HTMX, Alpine.js, or
similar frameworks to join in enhancing the BirdNET-Go application web UI. Your expertise can make
a real difference in how users engage with this app and connect with birdlife. If you have the
skills and a keen interest in contributing to a nature-focused project, I'd love to hear from you.

## Web Dashboard

<img src="doc/BirdNET-Go-dashboard.webp" />

For detailed installation instructions, see the [installation documentation](doc/wiki/installation.md). For securing your BirdNET-Go installation, see the [security documentation](doc/wiki/security.md). See [recommended hardware](doc/wiki/hardware.md) for optimal performance.

## Building
For instructions on how to build the project, see the [building documentation](doc/wiki/building.md).


## Usage

```bash
BirdNET-Go CLI

Usage:
  birdnet [command]

Available Commands:
  authors     Print the list of authors
  benchmark   Run performance benchmark
  directory   Analyze all *.wav files in a directory
  file        Analyze an audio file
  help        Help about any command
  license     Print the license of Go-BirdNET
  rangefilter Manage the range filter database
  realtime    Analyze audio in realtime mode
  support     Generate a support bundle

Flags:
      --debug               Enable debug output
  -h, --help                help for birdnet
      --locale string       Set the locale for labels. Accepts full name or 2-letter code. (default "finnish")
      --overlap float       Overlap value between 0.0 and 2.9 (High values like 2.7 enable Deep Detection)
      --sensitivity float   Sigmoid sensitivity value between 0.0 and 1.5 (default 1)
      --threshold float     Confidency threshold for detections, value between 0.1 to 1.0 (default 0.8)

Use "birdnet [command] --help" for more information about a command.

There is more detailed usage documentation at [Wiki](doc/wiki/guide.md)

## License

Creative Commons Attribution-NonCommercial-ShareAlike 4.0 International

## Authors

Tomi P. Hakala

Contributions by Hampus Carlsson, Jan Vrska, @twt--, @aster1sk, @hoover67

Please let me know if you are missing from contributors list!

BirdNET AI model by the K. Lisa Yang Center for Conservation Bioacoustics at the Cornell Lab of Ornithology in collaboration with Chemnitz University of Technology. Stefan Kahl, Connor Wood, Maximilian Eibl, Holger Klinck.

BirdNET label translations by Patrick Levin for BirdNET-Pi project by Patrick McGuire.