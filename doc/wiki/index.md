# BirdNET-Go Documentation

Welcome to the BirdNET-Go documentation. This index will help you navigate through all available resources for installing, configuring, and using BirdNET-Go.

## Getting Started

- [Frequently Asked Questions](faq.md) - Common questions, problems, and quick fixes
- [BirdNET-Go Guide](guide.md) - Overview and basic concepts
- [Installation Guide](installation.md) - Comprehensive installation instructions for all methods
- [Recommended Hardware](hardware.md) - Hardware recommendations for optimal performance

## Installation Methods

- [Docker Installation (Linux)](installation.md#recommended-method-installsh-linux) - Using the automated `install.sh` script
- [Docker Compose Installation](docker_compose_guide.md) - Setting up BirdNET-Go with Docker Compose
- [Manual Docker Installation](installation.md#manual-docker-installation-advanced-linux-only) - Advanced Docker setup
- [Manual Binary Installation](installation.md#manual-binary-installation-all-platforms) - Windows, macOS, and Linux binary installation

**Note**: Docker images are available from both [GitHub Container Registry](https://github.com/tphakala/birdnet-go/pkgs/container/birdnet-go) and [Docker Hub](https://hub.docker.com/r/tphakala/birdnet-go).

## Advanced Features

- [External Media (USB, SD, File Shares)](external-media.md) - Mounting removable and network storage for import and backup
- [Detection Pipeline Architecture](detection-pipeline.md) - How audio flows through multi-model inference, filtering, and action dispatch
- [ONNX Runtime Installation](onnx-runtime-installation.md) - Installing the default inference backend library for native/binary installs
- [OpenVINO Acceleration](openvino-acceleration.md) - Enabling Intel iGPU offload and OpenVINO CPU acceleration on containers and native installs
- [BirdNET Detection Pipeline](guide.md#birdnet-detection-pipeline) - Understanding how settings affect detections
- [BirdNET Range Filter](guide.md#birdnet-range-filter) - Location and time-based species filtering
- [Web Dashboard](guide.md#web-dashboard) - Using the visualization dashboard
- [Remote Internet Access](cloudflare_tunnel_guide.md) - Exposing BirdNET-Go to the internet securely
- [Weather Integration](guide.md#weather-integration) - Connecting to weather data providers
- [Audio Processing](guide.md#audio-processing) - Advanced audio processing capabilities
- [Deep Detection](guide.md#deep-detection) - Improving detection reliability
- [Live Audio Streaming](guide.md#live-audio-streaming) - Streaming audio from the web interface
- [Species-Specific Settings](guide.md#species-specific-settings) - Customized detection rules for specific species

## Integration & Security

- [Push Notifications](guide.md#push-notifications) - Discord, Telegram, Slack, and 20+ services
- [Discord Setup Guide](guide.md#discord-setup-guide) - Step-by-step Discord webhook configuration with rich embeds
- [MQTT Integration](guide.md#integration-options) - Connecting to IoT systems
- [BirdWeather API](guide.md#integration-options) - Sharing data with BirdWeather.com
- [Authentication](cloudflare_tunnel_guide.md#enabling-authentication) - Securing your BirdNET-Go instance
- [Cloudflare Tunnel Setup](cloudflare_tunnel_guide.md) - Detailed guide for secure internet access

## Privacy & Telemetry

- [Error Tracking & Telemetry](telemetry.md) - Optional privacy-first error tracking system
- [Privacy & Data Collection](telemetry-privacy.md) - Detailed privacy information and data protection
- [Telemetry Setup Guide](telemetry-setup.md) - Step-by-step configuration instructions

## Troubleshooting & Support

- [Frequently Asked Questions](faq.md) - Common questions, problems, and quick fixes
- [RTSP Troubleshooting](rtsp-troubleshooting.md) - Comprehensive guide for RTSP camera issues and configuration
- [Docker Troubleshooting](guide.md#docker-installation-troubleshooting) - Resolving common Docker issues
- [Support Script](guide.md#support-script) - Generating diagnostic information
- [Reporting Issues](guide.md#reporting-issues) - How to effectively report bugs

## Reference

- [Configuration Reference](configuration-reference.md) - Complete reference of every `config.yaml` setting with types and descriptions (auto-generated from source)
- [Command Line Interface](guide.md#command-line-interface) - Available commands and options
- [Detection Pipeline Flow](guide.md#birdnet-detection-pipeline) - How settings interact and affect detection results
- [Range Filter Commands](guide.md#inspection-and-debugging) - CLI commands for inspecting range filter results
- [Supported Languages](guide.md#supported-languages-for-species-labels) - Language options for species labels
- [Log Rotation](guide.md#log-rotation) - Managing log files
