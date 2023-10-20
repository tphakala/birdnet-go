# Go-BirdNet

Golang implementation of Birdnet Analyzer.

![image](go-birdnet-logo.webp)

Go-BirdNet is an application inspired by BirdNet Analyzer, developed by the K. Lisa Yang Center for Conservation Bioacoustics at the Cornell Lab of Ornithology and Chemnitz University of Technology. While the original BirdNet is based on Python, Go-BirdNet is built using Golang, aiming for simplified deployment across multiple platforms, from Windows PCs to single board computers like Raspberry Pi.

Currently, Go-BirdNet supports the analysis of WAV files only, support for other audio file formats is planned. Use of metadata model for location-based filtering is not yet implemented. This is very much of work in progress, and contributions and feedback are welcome.

## Dependencies

Go-BirdNet primarily relies on go-tflite by Yasuhiro Matsumoto (a.k.a. mattn). Go-tflite provides a Go binding for the TensorFlow Lite C API. Although go-tflite is documented to support only TensorFlow Lite version 2.2.0-rc3, I have successfully compiled it against version 2.14.0. As go-tflite is a C API binding compiled binary is not statically linked one, it is depending on libtensorflowlite_c.so to be present on deployment target system.

A crucial component is the BirdNet tflite model. After all, Go-BirdNet essentially serves as an interface to this model, with the core detection functionality residing within the BirdNet tflite model itself.

## Executable Distributions

to be done

## Compiling

### Building TensorFlow Lite

Build tflite with cmake
```
sudo apt-get install cmake
```

Clone tensorflow repository
```
git clone https://github.com/tensorflow/tensorflow.git tensorflow_src
```

Create cmake build directory
```
mkdir tflite_build
cd tflite_build
```

Run cmake
```
cmake ../tensorflow_src/tensorflow/lite
```

Build tflite, In the tflite_build directory do
```
cmake --build . -j
```

Copy compiled  libtensorflowlite_c.so to /usr/local/lib

### Building Go-BirdNet

Clone go-birdnet repository
```
https://github.com/tphakala/go-birdnet.git
```

Add CGO_CFLAGS and point it to directory you cloned tensorflow source in
```
export CGO_CFLAGS=-I$HOME/src/tensorflow/tensorflow_src
```

Build Go-BirdNet
```
cd go-birdnet
go build birdnet.go
```

## Usage

```
$ ./birdnet -h
Usage of ./birdnet:
  -input string
    	Path to the input audio file (WAV)
  -model string
    	Path to the model file (default "BirdNET_GLOBAL_6K_V2.4_Model_FP32.tflite")
  -overlap float
    	Overlap value between 0.0 and 2.9
  -sensitivity float
    	Sigmoid sensitivity value between 0.0 and 1.5 (default 1)
```

## License

Creative Commons Attribution-NonCommercial-ShareAlike 4.0 International