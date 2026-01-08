    ____  _          ___   ______________    ______
   / __ )(_)________/ / | / / ____/_  __/   / ____/___
  / __  / / ___/ __  /  |/ / __/   / /_____/ / __/ __ \
 / /_/ / / /  / /_/ / /|  / /___  / /_____/ /_/ / /_/ /
/_____/_/_/   \__,_/_/ |_/_____/ /_/      \____/\____/

This document is severly outdated, please see https://github.com/tphakala/birdnet-go/wiki
for up to date documentation,


Quick Start

File analysis
    birdnet-go file input.wav --threshold 0.1

Real-time analysis
    birdnet-go realtime

Configuration

BirdNET-Go accepts several settings from command line but main configuration
method is through configuration file which is created when birdnet-go is
first run.

On Linux and macOS default and primary location for configuration file
is $HOME/.config/birdnet-go/config.yaml for user specific deployment,
alternative location is /etc/birdnet-go/config.yaml for system wide deployment.

On Windows default and primary configuration file location is config.yaml
in birdnet-go.exe installation directory, secondary location is
C:\Users\username\AppData\Roaming\birdnet-go\config.yaml

You can also configure custom thresholds for specific species using
species_config.csv file. This file should be located with config.yaml in one
of supported config paths, example configuration file is included with this
readme.txt in birdnet-go release package.

File Analysis

File analysis is enabled by file flag, you can define additional options
such as BirdNET detection threshold, sensitivity or number of CPU threads
used by tflite. Default format for file analysis results is Raven table and
output is saved in output/inputfilename.wav.txt.

File analysis works best with WAV files which are recorded in 48k sample
rate, BirdNET-Go has very simple resampling algorithm which allows ingestion
of audio files with alternative sample rates but detection accuracy will be
degraded. Input file bit depths of 16, 24 and 32 (int) are supported.

Running a file analysis for single file

raspberry$ ./birdnet-go file soundscape.wav --threshold 0.1
Read config file: /etc/birdnet-go/config.yaml
BirdNET GLOBAL 6K V2.4 FP32 model initialized, using 4 threads of available
4 CPUs
Analysis completed, total time elapsed: 8 second(s)
Output written to output/soundscape.wav.txt

Real-Time Analysis

Real-time analysis requires an audio capture device from which audio is
ingested for analysis. The input audio format should be 16-bit, 22050
Hz. Stereo input is acceptable, as it is downmixed to one channel within
BirdNET-Go. Mismatched capture settings may reduce accuracy.

Real-time analysis operates by reading PCM audio into an internal buffer. This
buffer is then accessed by another thread as a sliding window buffer in
1.5-second intervals until 3 seconds of unprocessed audio is accumulated. These
3-second chunks are fed to the BirdNET model for analysis, and this process
repeats until the application is stopped.

Results of real-time analysis can be saved to a log file and to SQLite or MySQL
databases. The log file format is suitable for use as a chat log overlay in
Open Broadcaster Software for wildlife streams and similar applications. The
database format is suitable for a BirdNET-Pi-like interface, allowing for
the visualization of bird observations.

Setting up audio device

BirdNET-Go uses miniaudio library for audio device access, supported audio
backends for miniaudio are

Linux
- ALSA
- Pulse Audio
- JACK

Windows
- WASAPI
- DirectSound
- WinMM

macOS
- Core Audio

On Linux use of PulseAudio backend is recommended.

Configuring Pulse Audio on Linux

Make sure Pulse Audio is installed on your system and set Pulse Audio to output
16bit 22050 hz audio by editing /etc/pulse/daemon.conf to contain following

default-sample-format = s16le
default-sample-rate = 22050

Node and BirdNET settings

In config.yaml there is section for node and BirdNET analyzer specific settings

node:
  name: BirdNET-Go
  locale: en
  threads: 0
  timeas24h: true

Value of (node name) is saved in database for each detection which allows
identifying source node in multi node setups sharing same database.

Setting (node locale) controls which translations are used for common names
of birds in output. Valid locales are documented here Supported Locales

Setting (node threads) controls the number of CPU threads used by
the TensorFlow Lite runtime. A setting of 0 utilizes all available
CPU threads. Valid values range from 1 to the number of CPU cores on the
system. If the value exceeds the number of CPU cores available on the system,
it is capped at the system's CPU count.

Setting (node timeas24h: true) ensures that output timestamps are in the
24-hour format. Setting it to false is intended to use the 12-hour format,
but this feature is not yet implemented.

birdnet:
  sensitivity: 1.0
  threshold: 0.75
  overlap: 0.0
  latitude: 00.000
  longitude: 00.000

Setting (birdnet sensitivity) controls sigmoid sensitivity of prediction in
BirdNET model, valid values are from 0.0 to 1.5, higher value makes model
more sensitive.

Setting (birdnet threshold) controls minimum confidence of prediction result
to be recorded for output, valid values are from 0.0 to 1.0. Low value may
cause lot of incorrect detections, high value may cause model to miss many
detections.

Setting (birdnet overlap) is not used for real-time detection.

The (birdnet latitude) and (birdnet longitude) settings enable location-based
species occurrence filtering. This feature helps to exclude non-native species
from your region in the results, thereby reducing incorrect detections. Setting
these values to 00.000 disables location-based filtering. Valid values for
these settings are required if Birdweather uploads are enabled.

Configuring output

Configuration file has few settings which controls output of real-time
detection results

realtime:
  interval: 15
  processingtime: false
  audioexport:
    enabled: true
    path: clips/
    type: wav
  log:
    enabled: true
    path: birdnet.txt

Setting (interval: 15) reduces duplicate results by setting minimal interval
for reporting repeated species

Setting (processingtime: true) prints the time it took for BirdNET analysis
to process a 3-second audio chunk. These values should range from 50ms to
550ms, depending on the hardware you are running BirdNET-Go on. As long as
the processingtime is less than 1500ms, audio is processed quickly enough
to keep up with real-time capture and analysis.

Setting (audioexport enabled: true) allows 3-second audio clips containing
identified bird calls to be saved to disk. The supported audio formats
on Linux are WAV, FLAC, and MP3. On Windows, only WAV is supported due
to library constraints. The default path for audio clip exports is clips,
which is relative to the directory where BirdNET-Go is executed.

Setting (log enabled: true) saves the timestamp and common name of identified
birds to a log file, which can be used as a chat log overlay in OBS.

output:
  file:
    enabled: false
  sqlite:
    enabled: true
    path: birdnet.db
  mysql:
    enabled: false
    username: birdnet
    password: secret
    database: birdnet
    host: localhost
    port: 3306

Setting (output file enabled) does not apply to real-time detection.

Enabling the SQLite option (sqlite enabled: true) allows real-time detection
results to be stored in a SQLite database. The (sqlite path) setting determines
the location of the SQLite database. By default, the location is birdnet.db
in the directory where BirdNET-Go is executed.

Enabling the MySQL option (mysql enabled: true) allows real-time detection
results to be stored in a MySQL database. Please note that if both SQLite
and MySQL are set to enabled, only the SQLite output will be used.
