Real-time analysis requires an audio capture device from which audio is ingested for analysis. The input audio format should be 16-bit, 48000 Hz. Stereo input is acceptable, as it is downmixed to one channel within BirdNET-Go. Mismatching capture settings may reduce the accuracy of the analysis.

Real-time analysis is conducted by loading PCM audio into a memory buffer. This buffer is then accessed every 1.5 seconds by a dedicated thread, which continues until there are 3 seconds of unprocessed audio accumulated. These 3-second audio segments are subsequently fed into the BirdNET model for analysis. This process repeats continuously until the application is terminated.

Results of real-time analysis can be saved to a log file and to SQLite or MySQL databases. The log file format is suitable for use as a chat log overlay in [Open Broadcaster Software](https://obsproject.com/fi) for wildlife streams and similar applications. The database format is suitable for a BirdNET-Pi-like interface, allowing for the visualization of bird observations.

## Setting up audio device

BirdNET-Go uses miniaudio library for audio device access, supported audio backends for miniaudio are 

### Linux
* ALSA
* Pulse Audio
* JACK

### Windows
* WASAPI
* DirectSound
* WinMM

### macOS
* Core Audio

For Linux users, the PulseAudio backend is recommended. Additionally, Linux distributions that use PipeWire, specifically with the 'Pipewire-pulse' and 'libpulse' components, have also been tested and confirmed to work.

### Configuring Pulse Audio on Linux

Make sure Pulse Audio is installed on your system and set Pulse Audio to output 16bit 48000 hz audio by editing **/etc/pulse/daemon.conf** to contain following

```
default-sample-format = s16le
default-sample-rate = 48000
```

### Configuring Recording device properties on Windows

Make sure that recording device is set to 16 bit 48000 Hz mode, 2 channel source is downmixed to 1 channel internally in BirdNET-Go. I also recommend unchecking "Allow applications to take exclusive control of this device" especially if you plan to run OBS on same system.

![windows_recording_properties](https://github.com/tphakala/birdnet-go/assets/7030001/f994f01a-7614-428d-b7e5-eca274261889)

## Node and BirdNET settings

```
node:
  name: BirdNET-Go
  locale: en
  threads: 0
  timeas24h: true
```

Value of (**node name**) is saved in database for each detection which allows identifying source node in multi node setups sharing same database.

Setting (**node locale**) controls which translations are used for common names of birds in output. Valid locales are documented here [Supported Languages](guide.md#supported-languages-for-species-labels)

Setting (**node threads**) controls the number of CPU threads used by the TensorFlow Lite runtime. A setting of 0 utilizes all available CPU threads. Valid values range from 1 to the number of CPU cores on the system. If the value exceeds the number of CPU cores available on the system, it is capped at the system's CPU count.

Setting (**node timeas24h: true**) ensures that output timestamps are in the 24-hour format. Setting it to false is intended to use the 12-hour format, but this feature is not yet implemented.

```
birdnet:
  sensitivity: 1.0
  threshold: 0.75
  overlap: 0.0
  latitude: 00.000
  longitude: 00.000
```

Setting (**birdnet sensitivity**) controls sigmoid sensitivity of prediction in BirdNET model, valid values are from 0.0 to 1.5, higher value makes model more sensitive.

Setting (**birdnet threshold**) controls minimum confidence of prediction result to be recorded for output, valid values are from 0.0 to 1.0. Low value may cause lot of incorrect detections, high value may cause model to miss many detections.

Setting (**birdnet overlap**) is not used for real-time detection.

The (**birdnet latitude**) and (**birdnet longitude**) settings enable location-based species occurrence filtering. This feature helps to exclude non-native species from your region in the results, thereby reducing incorrect detections. Setting these values to 00.000 disables location-based filtering. Valid values for these settings are required if Birdweather uploads are enabled.

## Configuring output

Configuration file has few settings which controls output of real-time detection results

```
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
```

Interval setting reduces log flooding by setting a minimum interval, in seconds, for a repeating bird call to be logged. This flood filter also applies to SQL output.

Setting (**processingtime: true**) prints the time it took for BirdNET analysis to process a 3-second audio chunk. These values should range from 50ms to 550ms, depending on the hardware you are running BirdNET-Go on. As long as the processingtime is less than 1500ms, audio is processed quickly enough to keep up with real-time capture and analysis.

Setting (**audioexport enabled: true**) allows 3-second audio clips containing identified bird calls to be saved to disk. Only WAV audio format type is supported for now. The default path for audio clip exports is **clips**, which is relative to the directory where BirdNET-Go is executed.

Setting (**log enabled: true**) saves the timestamp and common name of identified birds to a log file, which can be used as a chat log overlay in OBS. 

```
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
```

Setting (**output file enabled**) does not apply to real-time detection.

Enabling the SQLite option (**sqlite enabled: true**) allows real-time detection results to be stored in a SQLite database. The (**sqlite path**) setting determines the location of the SQLite database. By default, the location is birdnet.db in the directory where BirdNET-Go is executed.

Enabling the MySQL option (**mysql enabled: true**) allows real-time detection results to be stored in a MySQL database. Please note that if both SQLite and MySQL are set to enabled, only the SQLite output will be used.

## BirdWeather support

BirdNET-Go supports BirdWeather API for uploading captured audio clips and BirdNET results to birdweather.com. BirdNET-Go uploads 3 second audio clips to BirdWeather

For Birdweather API access you need a token which can be requested by email from tim@birdweather.com. You can use following template for token request

```
Hi, Tim, and thank you so much for BirdWeather.com!

Below is the information I would like to use to request a BirdWeather ID

Latitude=
Longitude=
City= [ City ]
State= [ State ]
Country= [ Country ]


Thank you so much!
[ Your Name ]

Disclaimer: By requesting this BirdWeather ID, I acknowledge that my location
and recording data will be made public.
```

