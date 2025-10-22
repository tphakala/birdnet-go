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
  <a href="https://discord.gg/gcSCFGUtsd">
    <img src="https://img.shields.io/discord/1298738918239100939?style=flat-square&logo=discord&color=5865F2&label=Discord">
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

## Development Setup

For developers who want to contribute or build from source:

> See [CONTRIBUTING.md](CONTRIBUTING.md#step-1-install-task-runner) for more details.

```bash
# Clone the repository
git clone https://github.com/tphakala/birdnet-go.git
cd birdnet-go

# Install Task (if not already installed)
# Linux: sh -c "$(curl --location https://taskfile.dev/install.sh)" -- -d -b /usr/local/bin
# macOS: brew install go-task (assumes Homebrew is installed)

# Setup development environment (Linux apt-based or macOS with homebrew)
task setup-dev

# Build the project
task

# Start development server with hot reload
task dev_server # or "air realtime"
```

The `setup-dev` task will automatically install:

- Go 1.25
- Node.js LTS
- Build tools (gcc, git, wget, etc.)
- golangci-lint (Go linter)
- air (hot reload for Go)
- Frontend dependencies and Playwright browsers

## Web Dashboard

<img src="doc/BirdNET-Go-dashboard.webp" />

For detailed installation instructions, see the [installation documentation](doc/wiki/installation.md). For securing your BirdNET-Go installation, see the [security documentation](doc/wiki/security.md). See [recommended hardware](doc/wiki/hardware.md) for optimal performance.

There is more detailed usage documentation at [Wiki](doc/wiki/guide.md)

## Community

Join our [Discord server](https://discord.gg/gcSCFGUtsd) for support, discussions, and updates about BirdNET-Go!

## Related Projects

### Core & Extensions

- [BirdNET-Analyzer](https://github.com/birdnet-team/BirdNET-Analyzer) - Upstream project providing the BirdNET AI model for bird sound identification
- [BirdNET-Go Classifiers](https://github.com/tphakala/birdnet-go-classifiers) - Enhanced BirdNET classifiers including additional species

### System Integration

- [Cockpit BirdNET-Go](https://github.com/tphakala/cockpit-birdnet-go) - Web-based system management plugin for BirdNET-Go using Cockpit framework

### Migration Tools

- [BirdNET-Pi2Go](https://github.com/tphakala/birdnet-pi2go) - Database conversion tool for migrating from BirdNET-Pi to BirdNET-Go

### Hardware Solutions

- [BirdNET-Go ESP32 RTSP Microphone](https://github.com/Sukecz/birdnetgo-esp32-rtsp-mic) - ESP32-based RTSP streaming microphone for remote audio capture
- [ESP32 Audio Streamer](https://github.com/jpmurray/esp32-audio-streamer) - Alternative ESP32 RTSP streaming solution for BirdNET-Go audio input

## Contributing

**Want to contribute?** We welcome contributions from the community! 🎉

For comprehensive contributing guidelines, development setup, and workflow documentation, see [**CONTRIBUTING.md**](CONTRIBUTING.md).

### Quick Start for Contributors

**Experienced developers** can get started in 5 minutes:

```bash
git clone https://github.com/tphakala/birdnet-go.git && cd birdnet-go
task setup-dev  # One command installs everything (Go, Node.js, tools, git hooks)
air realtime    # Start developing with hot reload
```

**New to the project?** The [contributing guide](CONTRIBUTING.md) includes:

- 📋 [TL;DR Quick Start](CONTRIBUTING.md#tldr---quick-start-for-experienced-developers) - 5-minute setup
- 🔧 [Automated Environment Setup](CONTRIBUTING.md#development-environment-setup) - `task setup-dev` handles everything
- 📖 [Development Workflow](CONTRIBUTING.md#development-workflow) - Hot reload, git hooks, testing
- ⚖️ [License & Privacy](CONTRIBUTING.md#license-and-legal) - CC BY-NC-SA 4.0, privacy-by-design
- 💬 [Discord Community](https://discord.gg/gcSCFGUtsd) - Get help and discuss features

All contributions must follow:

- ✅ Privacy-by-design principles
- ✅ Code quality standards (automated via git hooks)
- ✅ CC BY-NC-SA 4.0 license terms

See [CONTRIBUTING.md](CONTRIBUTING.md) for complete details.

## License

Creative Commons Attribution-NonCommercial-ShareAlike 4.0 International

## Authors

Tomi P. Hakala

Contributions by Hampus Carlsson, Jan Vrska, @twt--, @aster1sk, @hoover67

Please let me know if you are missing from contributors list!

BirdNET AI model by the K. Lisa Yang Center for Conservation Bioacoustics at the Cornell Lab of Ornithology in collaboration with Chemnitz University of Technology. Stefan Kahl, Connor Wood, Maximilian Eibl, Holger Klinck.

BirdNET label translations by Patrick Levin for BirdNET-Pi project by Patrick McGuire.
