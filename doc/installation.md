# Installation

BirdNET-Go can be installed either using Docker or binary provided releases. Docker is the
preferred method, as it provides a self-contained and easily reproducible
environment. However, binary releases are convenient option for users who prefer not to install Docker.


## Docker

**Note**: Docker is currently only supported when running inside a Linux-based
host system.


### Installing Docker

To install Docker, follow the instructions in the [official installation guide](https://docs.docker.com/engine/install) for your operating system.

### Running BirdNET-GO with Docker - Simple setup


The command below will start a container with the latest version BirdNET-Go:

> docker run -ti -p 8080:8080 --device /dev/snd ghcr.io/tphakala/birdnet-go:latest

Once executed, the service can be reached at [localhost:8080](http://localhost:8080).


### Running BirdNET-GO with Docker - Normal setup
While the [simple](##Running-BirdNET-GO-with-Docker-Simple) example above works, it is highly likely that customizing the runtime settings more as well as enabling persistent storage is desirable. The docker run snippet below offers many more options:

```
docker run -ti \
  -p 8080:8080 \
  --env ALSA_CARD=<index/name>
  --env TZ="<TZ identifier>"
  --device /dev/snd \
  -v /path/to/config:/config \
  -v /path/to/data:/data \
  ghcr.io/tphakala/birdnet-go:latest
```

Summary of parameters:

| Parameter | Function |
| :----: | --- |
| `-p 8080` | BirdNET-GO webserver port. |
| `--env ALSA_CARD=<index/name>` | ALSA capture device to use. Find index/name of desired device by executing `arecord -l` on the host. [More info.](#deciding-alsa_card-value)|
| `--env TZ="TZ identifier"` | Timezone to use. See [wikipedia article](https://en.wikipedia.org/wiki/List_of_tz_database_time_zones#List) to find TZ identifier.|
| `--device /dev/snd` | Mounts in audio devices to the container. |
| `-v /config` | Config directory in the container. |
| `-v /data` | Data such as database and recordings. |

#### Example setup

To start BirdNET-GO, simply fill in the values of the parameters. Below is an example of how it might look:

```
docker run -ti \
  -p 8080:8080 \
  --env ALSA_CARD=0
  --env TZ="Europe/Stockholm"
  --device /dev/snd \
  -v $HOME/BirdNET-Go-Volumes/config:/config \
  -v $HOME/BirdNET-Go-Volumes/data:/data \
  ghcr.io/tphakala/birdnet-go:latest
```

#### Deciding ALSA_CARD value

Within the BirdNET-Go container, knowledge of the designated microphone is absent. Consequently, it is necessary to specify the appropriate ALSA_CARD environment variable. Determining the correct value for this variable involves the following steps on the host computer:
1. Open a terminal and execute the command `arecord -l` to list all available capture devices.

```
> arecord -l
**** List of CAPTURE Hardware Devices ****
card 0: PCH [Generic Analog], device 0: Analog [Analog]
  Subdevices: 1/1
  Subdevice #0: subdevice #0
card 0: PCH [Generic Analog], device 2: Alt Analog [Alt Analog]
  Subdevices: 1/1
  Subdevice #0: subdevice #0
card 1: Microphone [USB Microphone], device 0: USB Audio [USB Audio]
  Subdevices: 1/1
  Subdevice #0: subdevice #0
```
2. Identify the desired capture device. In the example above, cards 0 and 1 are available.
3. Specify the ALSA_CARD value when running the BirdNET-Go container. For instance, if the USB Microphone device is chosen, set `ALSA_CARD` to either `ALSA_CARD=1` or `ALSA_CARD=Microphone`.

## Binary releases

Ready to run binaries can be found in [releases](https://github.com/tphakala/BirdNET-Go/releases/) section. Unfortunately, not everything is contained inside the binary itself, meaning that certain dependencies must be installed on the host system first. One of them being TensorFlow Lite C library, see this [guide](building.md#install-tensorflow-lite-c-library) for more information.
