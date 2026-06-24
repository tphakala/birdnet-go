File analysis is enabled by **file** flag, you can define additional options such as BirdNET detection threshold, sensitivity or number of CPU threads used by tflite. Default format for file analysis results is Raven table and output is saved in _output/inputfilename.wav.txt_.

File analysis works best with WAV files which are recorded in 48k sample rate, BirdNET-Go has very simple resampling algorithm which allows ingestion of audio files with alternative sample rates but detection accuracy will be degraded. Input file bit depths of 16, 24 and 32 (int) are supported.

Running a file analysis for single file

```
raspberry$ ./birdnet-go file soundscape.wav --threshold 0.1
Read config file: /etc/birdnet-go/config.yaml
BirdNET GLOBAL 6K V2.4 FP32 model initialized, using 4 threads of available 4 CPUs
Analysis completed, total time elapsed: 8 second(s)
Output written to output/soundscape.wav.txt
```

Default Raven table output

```
raspberry$ cat output/soundscape.wav.txt
Selection       View    Channel Begin File      Begin Time (s)  End Time (s)    Low Freq (Hz)   High Freq (Hz)  Species Code    Common Name     Confidence
1       Spectrogram 1   1       soundscape.wav  0.0     3.0     0       15000   bkcchi  Black-capped Chickadee  0.9016
2       Spectrogram 1   1       soundscape.wav  3.0     6.0     0       15000   bkcchi  Black-capped Chickadee  0.2293
4       Spectrogram 1   1       soundscape.wav  9.0     12.0    0       15000   houfin  House Finch     0.7025
7       Spectrogram 1   1       soundscape.wav  18.0    21.0    0       15000   blujay  Blue Jay        0.4036
8       Spectrogram 1   1       soundscape.wav  21.0    24.0    0       15000   blujay  Blue Jay        0.2557
10      Spectrogram 1   1       soundscape.wav  27.0    30.0    0       15000   merlin  Merlin  0.1787
12      Spectrogram 1   1       soundscape.wav  33.0    36.0    0       15000   daejun  Dark-eyed Junco 0.4388
13      Spectrogram 1   1       soundscape.wav  36.0    39.0    0       15000   daejun  Dark-eyed Junco 0.2882
14      Spectrogram 1   1       soundscape.wav  39.0    42.0    0       15000   houfin  House Finch     0.1649
15      Spectrogram 1   1       soundscape.wav  42.0    45.0    0       15000   daejun  Dark-eyed Junco 0.8249
18      Spectrogram 1   1       soundscape.wav  51.0    54.0    0       15000   houfin  House Finch     0.1684
19      Spectrogram 1   1       soundscape.wav  54.0    57.0    0       15000   houfin  House Finch     0.6576
21      Spectrogram 1   1       soundscape.wav  60.0    63.0    0       15000   daejun  Dark-eyed Junco 0.5821
24      Spectrogram 1   1       soundscape.wav  69.0    72.0    0       15000   houfin  House Finch     0.2088
25      Spectrogram 1   1       soundscape.wav  72.0    75.0    0       15000   houfin  House Finch     0.5951
27      Spectrogram 1   1       soundscape.wav  78.0    81.0    0       15000   houfin  House Finch     0.1680
28      Spectrogram 1   1       soundscape.wav  81.0    84.0    0       15000   hawfin  Hawfinch        0.1956
29      Spectrogram 1   1       soundscape.wav  84.0    87.0    0       15000   houfin  House Finch     0.1953
31      Spectrogram 1   1       soundscape.wav  90.0    93.0    0       15000   amegfi  American Goldfinch      0.3799
32      Spectrogram 1   1       soundscape.wav  93.0    96.0    0       15000   houfin  House Finch     0.2400
33      Spectrogram 1   1       soundscape.wav  96.0    99.0    0       15000   amegfi  American Goldfinch      0.4648
35      Spectrogram 1   1       soundscape.wav  102.0   105.0   0       15000   houfin  House Finch     0.4192
38      Spectrogram 1   1       soundscape.wav  111.0   114.0   0       15000   engine  Engine  0.5252
39      Spectrogram 1   1       soundscape.wav  114.0   117.0   0       15000   engine  Engine  0.1538
40      Spectrogram 1   1       soundscape.wav  117.0   120.0   0       15000   amegfi  American Goldfinch      0.3417
```