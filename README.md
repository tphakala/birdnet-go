# BirdNET-Go

<p align="center">
  <img src="doc/BirdNET-Go-logo.webp" />
</p>
<p align="center">
<a href="https://creativecommons.org/licenses/by-nc-sa/4.0/">
 <img src="https://badgen.net/badge/License/CC-BY-NC-SA%204.0/green">
 </a>
 <img src="https://badgen.net/badge/OS/Linux%2C%20Windows%2C%20macOS/blue">
</p>

BirdNET-Go is an AI solution for continuous avian monitoring and identification

- 24/7 realtime bird song analysis of soundcard capture, analysis output to log file, SQLite or MySQL
- Utilizes BirdNET AI model trained with more than 6500 bird species
- Local processing, Internet connectivity not required
- Easy to use Web user interface for data visualisation
- BirdWeather.com API integration
- Realtime log file output can be used as overlay in OBS for bird feeder streams etc.
- Minimal runtime dependencies, BirdNET Tensorflow Lite model is embedded in compiled binary
- Runs on Windows, Linux and macOS
- Low resource usage, works on Raspberry Pi 3 and equivalent 64-bit single board computers

## Help Improve BirdNET-Go

I am seeking web developer with experience in Go HTML templating, Tailwind CSS, HTMX, Alpine.js, or
similar frameworks to join in enhancing the BirdNET-Go application web UI. Your expertise can make
a real difference in how users engage with this app and connect with birdlife. If you have the
skills and a keen interest in contributing to a nature-focused project, I'd love to hear from you.

## Web Dashboard

<img src="doc/BirdNET-Go-dashboard.webp" />

## Installation

For detailed installation instructions, see the [installation documentation](doc/installation.md).


## Building
For instructions on how to build the project, see the [building documentation](doc/building.md).


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

There is more detailed usage documentation at wiki https://github.com/tphakala/birdnet-go/wiki

## License

Creative Commons Attribution-NonCommercial-ShareAlike 4.0 International

## Authors

Tomi P. Hakala

BirdNET AI model by the K. Lisa Yang Center for Conservation Bioacoustics at the Cornell Lab of Ornithology in collaboration with Chemnitz University of Technology. Stefan Kahl, Connor Wood, Maximilian Eibl, Holger Klinck.

BirdNET label translations by Patrick Levin for BirdNET-Pi project by Patrick McGuire.
