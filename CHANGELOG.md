# Changelog

All notable changes to this project will be documented in this file.

## [0.5.5] - 2024-06-09

### 🚀 Features

- _(audio)_ Support for multiple RTSP streams
- _(birdnet)_ Range filter model is now selectable between latest and previous "legacy" version
- _(birdnet)_ Added "birdnet-go range print" command which lists all species included by range filter model
- _(birdnet)_ BirdNET overlap setting impacts now realtime process also

### 🐛 Bug Fixes

- _(privacy)_ Fix defaults for privacy and dog bark filters and fix incorrect setting names in default config.yaml
- _(privacy)_ Do not print dog bark detections if dog bark filter is not enabled
- _(privacy)_ Fix printf declaration for human detection confidence reporting
- _(audio)_ Yield to other goroutines in file_utils, policy_age, and policy_usage
- _(build)_ Linux/arm64 cross-compilation in docker build

### 🚜 Refactor

- _(audio)_ Refactor analysis and capture buffers to support multiple individual buffers for different audio sources
- _(audio)_ Move RTSP code to rtsp.go
- _(rtsp)_ Update default RTSP URLs to an empty slice
- _(rtsp)_ Update RTSP stream URLs example in config.yaml
- _(privacy)_ Make pricacy filter and dog bark filter source specific
- _(build)_ Move buildDate variable to main.go

### 🏗️ Building

- _(deps)_ Bump github.com/spf13/viper from 1.18.2 to 1.19.0
- _(deps)_ Bump github.com/shirou/gopsutil/v3 from 3.24.4 to 3.24.5

## [0.5.4] - 2024-06-01

### 🚀 Features

- _(audio)_ Disk usage based audio clip retention policy, enabled by default with 80% disk usage treshold
- _(conf)_ Privacy filter Confidence threshold setting
- _(conf)_ Dog bark filter Confidence threshold setting
- _(conf)_ Dog bark filter time to remember bark setting

### 🐛 Bug Fixes

- _(webui_) Fix Settings interface load error

### 🚜 Refactor

- _(conf)_ Refactor configuration package to improve settings handling, easier access to settings in code
- _(audio)_ Audio clip retention policy setting: none, age, usage
- _(audio)_ Age base audio clip retention policy accepts time in days, weeks, months and years instead of hours
- _(conf)_ Many settings renamed

### ⚙️ Miscellaneous Tasks

- Update go.mod with github.com/mitchellh/mapstructure v1.5.0

## [0.5.3] - 2024-05-21

### 🚀 Features

- _(birdweather)_ Added location fuzzing support for BirdWeather uploads, requires support for BirdWeather.com
- _(audio)_ Audio source device is now user configurable

### 🐛 Bug Fixes

- _(audio)_ Audio clip extraction fixed for occassional non-contiguous clips

### 🚜 Refactor

- _(conf)_ Move default config file from .go to .yaml and add proper default value initialization
- _(conf)_ Update audio export settings in updateconfig.go and realtime.go

### 🏗️ Building

- _(deps)_ Bump golang.org/x/text from 0.14.0 to 0.15.0
- _(deps)_ Bump golang.org/x/crypto from 0.22.0 to 0.23.0
- _(deps)_ Bump github.com/prometheus/client_golang

### ⚙️ Miscellaneous Tasks

- Update go version to 1.22.3
- Update golang version to 1.22.3
- Bump HTMX version from 1.9.11 to 1.9.12
- Bump daisyUI to 4.11.1
- Update custom.css to fix theme controller styles
- Update tailwindcss to v3.4.3
- Hide "Detections" column on smaller screens
- Update audio buffer initialization in realtime analysis
- Remove unused import and struct field in audiobuffer.go

## [0.5.2] - 2024-05-01

### 🐛 Bug Fixes

- File analysis restored
- Improve audio buffer write function time keeping
- _(birdweather)_ Improve handling of HTTP Responses in UploadSoundscape to prevent possible panics
- _(datastore)_ Refactor datastore Get, Delete and Save methods for efficient transaction and error handling
- _(tests)_ Refactor createDatabase function in interfaces_test.go for improved error handling
- _(datastore)_ Refactor GetClipsQualifyingForRemoval method in interfaces.go for improved input validation and error handling
- Refactor ClipCleanupMonitor function for improved error handling and logging
- _(birdweather)_ Fixed PCM to WAV encoding
- _(birdweather)_ Fixed PCM to WAV encoding
- _(birdweather)_ Increase HTTP timeout to 45 seconds
- _(utils)_ Do not report root user as missing from audio group
- _(audio)_ Fix default audio device reporting

### 💄 Enhancement

- _(audio)_ Print selected audio capture device on realtime mode startup
- _(startup)_ Enhance realtime mode startup message with system details to help troubleshooting

### 🚜 Refactor

- _(telemetry)_ Move Prometheus metrics to dedicated package and add pprof debug
- _(conf)_ Remove unused Context struct from internal/conf/context.go
- _(processor)_ Update range filter action to handle error when getting probable species

### 🏗️ Building

- _(deps)_ Bump golang.org/x/crypto from 0.21.0 to 0.22.0
- _(deps)_ Bump google.golang.org/protobuf from 1.32.0 to 1.33.0
- _(deps)_ Bump golang.org/x/net from 0.21.0 to 0.23.0
- _(go)_ Bump Go version from 1.21.6 to 1.22.2 in go.mod
- _(deps)_ Bump labstack echo version from 4.11.4 to 4.12.0
- _(deps)_ Bump gorm.io/gorm from 1.25.9 to 1.25.10
- _(deps)_ Bump github.com/gen2brain/malgo from 0.11.21 to 0.11.22

### ⚙️ Miscellaneous Tasks

- Fix linter errors
- Fix linter errors

### Github

- _(workflow)_ Add tensorflow dependencies to golangci-lint

## [0.5.1] - 2024-04-05

### 🐛 Bug Fixes

- _(birdnet)_ Make location filter threshold as configurable value under BirdNET node
- _(mqtt)_ Fix CodeRabbit magled code

### 🏗️ Building

- _(deps)_ Bump gorm.io/gorm from 1.25.8 to 1.25.9

## [0.5.0] - 2024-03-30

### 🚀 Features

- Privacy filter to discard audio clips with human vocals
- Save all BirdNET prediction results into table Results
- _(audio)_ Check user group membership on audio device open failure and print instructions for a fix
- _(docker)_ Added support for multiplatform build
- _(conf)_ New function to detect if running in container

### 🐛 Bug Fixes

- _(docker)_ Install ca-certificates package in container image
- _(capture)_ Set capture start to 5 seconds before detection instead of 4 seconds
- _(capture)_ Increase audio capture length from 9 to 12 seconds
- _(rtsp)_ Wait before restarting FFmpeg and update parent process on exit to prevent zombies

### 💄 Enhancement

- _(database)_ Switched sqlite journalling to MEMORY mode and added database optimize on closing
- _(workflow)_ Update GitHub Actions workflow to build and push Docker image to GHCR
- _(workflow)_ Update Docker actions versions
- _(workflow)_ Support multiplatform build with github actions
- _(docker)_ Add ffmpeg to container image
- _(labels)_ Add Greek (el) translations by @hoover67
- _(ui)_ Improve spectrogram generation to enable lazy loading of images
- _(make)_ Improve make file

### 🚜 Refactor

- Moved middleware code to new file
- Improved spectrogram generation
- Moved middleware code to new file
- _(database)_ Move save to common interface and change it to use transaction
- _(analyser)_ BirdNET Predict function and related types
- _(audio)_ Stopped audio device is now simply started again instead of full audio context restart
- _(audio)_ Disabled PulseAudio to prioritise audio capture to use ALSA on Linux
- _(audio)_ Set audio backend to fixed value based on OS
- _(config)_ Refactor RSTP config settings
- _(processor)_ Increase dog bark filter scope to 15 minutes and fix log messages
- _(rtsp)_ Improve FFmpeg process restarts and stopping on main process exit
- _(labels)_ Update makefile to zip labels.zip during build and have label files available in internal/birdnet/labels to make it easier to contribute language updates
- _(audio)_ Improve way start time of audio capture is calculated

### 📚 Documentation

- _(capture)_ Add documentation to audiobuffer.go
- Add git-cliff for changelog management
- _(changelog)_ Update git cliff config

### 🎨 Styling

- Remove old commented code
- _(docker)_ Removed commented out code

### 🏗️ Building

- _(deps)_ Add zip to build image during build
- _(deps)_ Bump gorm.io/driver/mysql from 1.5.4 to 1.5.6
- _(deps)_ Bump gorm.io/gorm from 1.25.7 to 1.25.8
- _(makefile)_ Update makefile
- _(makefile)_ Fix tensorflow lite lib install step
- _(makefile)_ Fix tflite install

### ⚙️ Miscellaneous Tasks

- _(assets)_ Upgrade htmx to 1.9.11

### Github

- _(workflow)_ Add windows build workflow
- _(workflow)_ Updated windows build workflow
- _(workflow)_ Add go lint workflow
- _(workflow)_ Remove obsole workflows
- _(workflow)_ Add build and attach to release workflow
- _(workflow)_ Update release-build.yml to trigger workflow on edited releases

## [0.3.0] - 2023-11-04

### 🚀 Features

- Added directory command
- Config file support
- Config file support

### 🐛 Bug Fixes

- Estimated time remaining print fixed
- Start and end time fix for stdout

<!-- generated by git-cliff -->
