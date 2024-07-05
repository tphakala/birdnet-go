# List of Recommended Hardware
BidrNet-Go works with many many different setups. This is meant to give an overview of what has been tried and found working. 
## Microphones
Microphones begin at a few Euros, but you can easily spend several hundred euros on a microphone, if you like. Generally, we are usually looking for omnidirectional microphones with a low signal-noise-ratio. 
### Low Cost
Many electronics stores carry cheap clip-on microphones in the range of 5-10 €. This usually have quite some noise, but I have found them to be good enough for recognizing birds in a setting where their song is loud and clear. 
### Value for Money
A great value for money attempt is soldering your own microphone, using either a EM-272 (10-15 €) or a AOM-5024 (1-5 €) microphone capsule. These, while sometimes difficult to source, offer more than decent quality. And when self-soldering these microphones, we can even take some measures to make them more wether-proof. Detailed instructions are available [here](https://github.com/mcguirepr89/BirdNET-Pi/discussions/39#discussioncomment-2180372) ([archived version](https://archive.ph/P23Ac)). 
## USB Sound Cards
Many electronics stores carry simple USB sound cards, sometimes specifically targeted towards Raspberry Pi usage. Go and ask at your local electronics store! Most USB sound cards use the same chipset, compatibility usually is great. 
**Note:** Depending on your sound card layout and your microphone's 3.5 mm connector, you may need an additional adaptar for your microphone to be recognized as a microphone. 
## Computing Devices
With BirdNet-Go being available as a docker container, it will work on a huge variety of devices. Thanks to the ability of analyzing RTSP audio streams, you can also run it on a central home server and feed in audio from different sources.
## Setting up RTSP audio streams
Instructions for setting up an RTSP stream on a device (e. g. a Raspberry Pi 0W) can be found [here](https://github.com/tphakala/birdnet-go/discussions/224#discussioncomment-9837887). 
