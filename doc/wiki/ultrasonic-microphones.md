# Ultrasonic Microphones for Bat Detection

Bat echolocation calls are ultrasonic, typically spanning roughly 20 kHz to 120+ kHz depending on the species. Standard microphones and 48 kHz audio interfaces roll off above 20 to 24 kHz and cannot capture these calls.

This guide compares ultrasonic-capable USB microphones, XLR analog microphones, and proven USB audio interfaces for running bat detection in BirdNET-Go. For compute, storage, and general bird-recording hardware, see [Recommended Hardware](hardware.md).

## BirdNET-Go Capture Requirements

- **Supported high capture sample rates:** 96 kHz, 192 kHz, 256 kHz, and 384 kHz (local capture uses mono; stereo devices are downmixed to mono).
- **Classifier requirements:**
  - **BattyBirdNET:** 96 kHz minimum, **192 kHz recommended** (256 kHz or 384 kHz gives full headroom for high-frequency species).
  - **BSG-BAT:** 192 kHz minimum, **384 kHz recommended**.
- **Platform support:** High-sample-rate local sound-card capture (above 48 kHz) uses exclusive ALSA mode on **Linux** (including Raspberry Pi and Docker installs via `install.sh`).
- **Clip export format:** Bat detections captured above 48 kHz are always saved losslessly as **FLAC** (default) or **WAV** via `realtime.audio.export.ultrasonictype`. See [Audio Formats](audio-formats.md).

## Quick Comparison

_Prices are approximate retail estimates (excluding shipping) and vary by retailer, VAT/taxes, exchange rates, and stock._

### USB Microphones

| Microphone                                                    | Max Rate | Approx. Price                    | Highlights / Caveats                                                 |
| ------------------------------------------------------------- | -------- | -------------------------------- | -------------------------------------------------------------------- |
| [AudioMoth](#audiomoth)                                       | 384 kHz  | ~$100                            | Micro-USB (USB-mic firmware); standard case has no USB opening       |
| [AudioMoth USB Microphone](#audiomoth-usb-microphone)         | 384 kHz  | ~$80 (+$30 case)                 | Micro-USB; optional splashproof USB mic case                         |
| [Dodotronic UltraMic 384K PRO](#dodotronic-ultramic-384k-pro) | 384 kHz  | ~€140 to €155                    | USB-C; audible + ultrasonic (~190 kHz); optional waterproof membrane |
| [Dodotronic Avicantus](#dodotronic-avicantus)                 | 192 kHz  | ~€69                             | USB; audible focus (ultrasonic unverified); 3.5 mm ext. mic input    |
| [Vleermic 384kHz](#vleermic-384khz)                           | 384 kHz  | ~€53                             | USB-C (UAC2); 0.1 to 150 kHz; not rainproof                          |
| [Pippyg](#pippyg)                                             | 384 kHz  | ~£15 to £20 DIY / ~€75 pre-built | Micro-USB (`pippyg_mic.uf2`); enclose outdoors                       |
| [Griff and Griff Mini](#griff-and-griff-mini)                 | 384 kHz  | ~£5 to £10 (DIY)                 | Micro-USB; bare PCB; enclose outdoors                                |

### XLR and Analog Microphones

_Require a 192 kHz audio interface such as a [Focusrite Scarlett Solo or 2i2](#focusrite-scarlett-solo-and-2i2-3rd-and-4th-gen) (~$100 to $220). All support combined bird + bat capture._

| Microphone                                                         | Frequency Range                          | Approx. Price | Highlights / Caveats                                           |
| ------------------------------------------------------------------ | ---------------------------------------- | ------------- | -------------------------------------------------------------- |
| [Clippy Ultra XLR](#clippy-ultra-xlr)                              | 20 Hz to 110 kHz                         | ~£70 / ~€92   | 12 to 48 V phantom; proven bird + bat; shelter outdoors        |
| [Sonorous Objects SO.104](#sonorous-objects-so104-ultrasonic-omni) | 20 Hz to 70 kHz                          | ~$90          | Standard 48 V XLR, Primo EM258; shelter outdoors               |
| [Sonorous Objects SO.4](#sonorous-objects-so4-ultrasonic-omni)     | 20 Hz to 70 kHz                          | ~$110         | 3.5 mm PIP or proprietary 48 V XLR cable; shelter outdoors     |
| [Rode NT5 (cardioid)](#rode-nt5-studio-microphone)                 | 20 Hz to 20 kHz rated (tested to 96 kHz) | ~$229 / ~€189 | 48 V XLR studio mic; narrow ultrasonic pattern; sheltered only |

## USB Ultrasonic Microphones

USB ultrasonic microphones integrate the sensor, preamp, and high-rate analog-to-digital converter in one unit. They plug directly into a Linux host or Raspberry Pi without an external audio interface, and most support **384 kHz**, which covers the full recommended sample rate for both BattyBirdNET and BSG-BAT.

### AudioMoth

- **Where to buy:** [GroupGets](https://groupgets.com/products/audiomoth) (US / worldwide) and [LabMaker](https://www.labmaker.org/) (EU / worldwide official distributor)
- **Approx. price:** ~$100 USD (~€95 to €105)
- **Max sample rate:** 384 kHz (configurable across 48, 96, 192, 256, and 384 kHz)
- **Connection:** Micro-USB (bus-powered in USB microphone mode)
- **Sensor:** Analog MEMS microphone with adjustable gain (hardware v1.2.0+ also includes a 3.5 mm jack for external plug-in-power microphones)

The standard AudioMoth from Open Acoustic Devices is primarily a battery-powered standalone acoustic logger with a microSD slot, but it can be flashed with the official **AudioMoth USB Microphone firmware** using the AudioMoth Flash App. Once flashed, it enumerates as a standard USB audio input device on Linux and Raspberry Pi.

Sample rate, gain, and filter settings are configured with the AudioMoth USB Microphone App and stored in flash memory so they persist across reboots and unplugs. Because you can reflash it back to standard AudioMoth firmware at any time, it is a flexible choice if you also do portable field logging.

**Enclosure limitation for USB use:** The standard AudioMoth enclosure (IPX7 logger case) is sealed for standalone battery operation and **does not have a USB port opening**, so it cannot be closed around the device while a USB cable is plugged in. If you want an AudioMoth with a protective case for BirdNET-Go, use the [AudioMoth USB Microphone](#audiomoth-usb-microphone) variant with the [AudioMoth USB Microphone Case](https://www.openacousticdevices.info/product-page/audiomoth-usb-microphone-case) instead.

### AudioMoth USB Microphone

- **Where to buy:** [GroupGets](https://groupgets.com/products/audiomoth-usb-microphone) (US / worldwide) and [LabMaker](https://www.labmaker.org/) (EU / worldwide); dedicated case from [Open Acoustic Devices](https://www.openacousticdevices.info/product-page/audiomoth-usb-microphone-case), GroupGets, or LabMaker
- **Approx. price:** ~$80 USD (~€75 to €85); optional [AudioMoth USB Microphone Case](https://www.openacousticdevices.info/product-page/audiomoth-usb-microphone-case) ~$30 USD (~€30)
- **Max sample rate:** 384 kHz (16-bit; configurable via AudioMoth USB Microphone App)
- **Connection:** Micro-USB (bus-powered, no batteries)
- **Sensor:** Same analog MEMS microphone and preamp front-end as AudioMoth 1.2.0, plus a 3.5 mm jack for external plug-in-power microphones or hydrophones

The AudioMoth USB Microphone is a smaller (35 x 40 mm), dedicated streaming-only variant of the AudioMoth without a battery holder or microSD slot. It ships ready for USB microphone use, plugs directly into a Raspberry Pi or Linux PC, and exposes the same full-spectrum capture up to 384 kHz. Like the AudioMoth, sample rate and gain are configured via the AudioMoth USB Microphone App and persist in flash.

If you need a protective enclosure for USB operation, this variant is the right choice over the standard AudioMoth: the official [AudioMoth USB Microphone Case](https://www.openacousticdevices.info/product-page/audiomoth-usb-microphone-case) is built specifically for this board (it does not fit the standard AA-battery AudioMoth), leaves the USB port accessible with a cable strain-relief loop, and protects the capsule behind a splashproof Gore acoustic vent. Because the case is splashproof rather than waterproof (and the acoustic vent slightly attenuates the highest frequencies), still mount it sheltered from direct rain with the microphone facing downward for 24/7 outdoor installations.

### Dodotronic UltraMic 384K PRO

- **Where to buy:** [Dodotronic](https://www.dodotronic.com/product/ultramic384k-pro/) (direct from Italy) and European wildlife retailers such as [Veldshop](https://www.veldshop.nl/en/)
- **Approx. price:** ~€140 direct from Dodotronic (~€151 at EU retailers, depending on VAT and membrane option)
- **Max sample rate:** 384 kHz (192 kHz bandwidth, 190 kHz low-pass filter)
- **Connection:** USB-C, USB 2.0 full-speed, driverless UAC 1.1
- **Sensor:** Wideband omnidirectional MEMS with low-noise analog front-end (-41.5 dBFS @ 94 dB SPL, ~29.5 g)

The UltraMic 384K PRO is Dodotronic's current flagship wideband digital audio and ultrasonic USB microphone. It delivers a flat response across both audible and ultrasonic frequencies, making it well suited for running bird classifiers (BirdNET, Perch) and bat classifiers (BattyBirdNET, BSG-BAT) simultaneously from a single 384 kHz USB source.

It is offered in two versions:

- **With waterproof membrane:** Recommended for outdoor and humid deployments. Mount the microphone with the sensor facing downward and shield it from direct driving rain. The membrane introduces a slight sensitivity reduction at the very top of the ultrasonic band.
- **Without membrane:** Maximum ultrasonic sensitivity for clean, indoor, or fully sheltered installations.

Other models in Dodotronic's UltraMic family (such as the UltraMic 384K EVO at ~€280 and UltraMic 250K EVO) also use driverless UAC USB audio and work similarly under Linux ALSA.

### Dodotronic Avicantus

- **Where to buy:** [Dodotronic](https://www.dodotronic.com/product/avicantus/) (direct; check current stock/availability)
- **Approx. price:** ~€69
- **Max sample rate:** 192 kHz (16-bit, 24-bit, or 32-bit stereo)
- **Connection:** USB (bus-powered, class-compliant)
- **Inputs:** Dual built-in preamplified microphones plus an adjustable-gain 3.5 mm analog microphone input jack and a direct hardware-monitoring headphone output

Avicantus is a compact, budget-friendly 192 kHz USB microphone and portable interface from Dodotronic. Its 192 kHz capture rate meets the recommended rate for BattyBirdNET and the minimum rate for BSG-BAT (BirdNET-Go automatically downmixes stereo capture to mono).

**Important caveat for bat detection:** Dodotronic markets Avicantus primarily for high-resolution audible wildlife recording (such as birds and soundscapes) rather than as a dedicated ultrasonic bat microphone, and does not publish an ultrasonic frequency response specification for the built-in capsules. Its 3.5 mm analog input lets you connect external plug-in-power microphones (such as an ultrasonic-capable 3.5 mm capsule), though if guaranteed ultrasonic sensitivity out of the box is your goal, a dedicated ultrasonic microphone such as the UltraMic 384K PRO, AudioMoth USB, or Vleermic 384kHz is a safer choice.

### Vleermic 384kHz

- **Where to buy:** [Veldshop](https://www.veldshop.nl/en/vleermic-384khz-ultrasonic-microphone.html) (Netherlands / EU shipping)
- **Approx. price:** ~€53 incl. 21% VAT (~€44 excl. VAT)
- **Max sample rate:** 384 kHz
- **Frequency response:** 0.1 to 150 kHz (192 kHz Nyquist ceiling)
- **Connection:** USB-C, standard USB Audio Class 2 (UAC2), 5 V / 27 mA bus-powered
- **Size and weight:** 40 x 21 x 13 mm, 5 g

The Vleermic 384kHz is an affordable, ultra-compact plug-and-play UAC2 ultrasonic microphone that requires no proprietary drivers on Linux, macOS, Windows, Android, or iOS. It has been confirmed working with BirdNET-Go on Raspberry Pi 5 over Linux ALSA.

Two practical caveats noted by the manufacturer:

- **Not weatherproof:** It is not designed for rain and needs a dry, sheltered housing for outdoor deployments.
- **RF sensitivity:** It can pick up radio interference from nearby cellular transmitters or smartphones, so keep it physically separated from cellular modems or high-power radios.

### Pippyg

- **Where to buy:**
  - **DIY self-build:** Project files, firmware, and parts links (PCBWay + DigiKey) on [pippyg.com](https://www.pippyg.com/pippyg.html)
  - **Pre-built (Pippyg 2):** [Apodemus](https://www.apodemus.eu/en/pipistrelle) and UK/EU ecological equipment retailers (such as Wildlife Services and NHBS)
- **Approx. price:** ~£15 to £20 in DIY parts, or ~€75 pre-built for the **Pippyg 2**
- **Max sample rate:** 384 kHz (16-bit)
- **Connection:** Micro-USB (for USB microphone mode and firmware flashing; USB-C on Apodemus Pippyg 2)

Pippyg is a low-cost, Raspberry Pi Pico / RP2040-based static bat detector designed by Phil Atkin. By default it operates as a standalone microSD recorder, but flashing the **`pippyg_mic.uf2`** firmware switches it so that whenever it is powered via USB it boots as a **384 kHz / 16-bit USB ultrasonic microphone** (equivalent to a Griff). You can reflash it back to `pippyg.uf2` for battery-powered standalone field surveys at any time.

To reflash, short the two "Reflash" pads while plugging in micro-USB so the board mounts as an `RPI-RP2` drive, then copy the `.uf2` file onto it. Keep moisture and dust out of the microphone hole and enclose the board for outdoor use.

### Griff and Griff Mini

- **Where to buy:** DIY self-build; instructions and firmware on [pippyg.com](https://www.pippyg.com/griff.html), blank PCBs and solder paste stencils via [PCBWay](https://www.pcbway.com/project/shareproject/Griff_Mini_d4d7cbaa.html), electronic components via DigiKey, and a standard Raspberry Pi Pico
- **Approx. price:** ~£5 to £10 per unit in parts when building small batches (e.g., 10 PCBs for ~$5 plus ~$10 stencil and shipping, plus components and Pico)
- **Max sample rate:** 384 kHz (16-bit, fixed rate)
- **Connection:** Micro-USB (bus-powered, class-compliant USB audio)

Griff and Griff Mini are dedicated USB ultrasonic microphones from the same Pippyg family, running `pipistrelle-usb-microphone.uf2` on a Raspberry Pi Pico. They operate out of the box as 384 kHz USB microphones with no batteries or SD card.

The two boards are electrically and functionally identical:

- **Griff:** Original bat-shaped PCB designed to be worn with a brooch pin.
- **Griff Mini:** Compact rectangular PCB with corner mounting holes (better suited to fixed mounting in an outdoor BirdNET-Go enclosure) and a slightly less cramped component layout.

The designer recommends sticking with genuine micro-USB Raspberry Pi Pico boards rather than low-cost USB-C clones, whose USB-C implementations can be unreliable. Both designs are bare PCBs and require weather protection for outdoor installation.

## XLR and Analog Ultrasonic Microphones

Analog XLR microphones do not contain a USB digitizer; they connect to a high-sample-rate USB audio interface (typically **192 kHz**) with **+48 V phantom power**. This setup costs more and caps at the interface's sample rate (usually 192 kHz rather than 384 kHz), but it offers clean preamplification, long balanced XLR cable runs without USB length limits, and strong dual-purpose performance for **simultaneous bird and bat monitoring** on a single input.

### Clippy Ultra XLR

- **Where to buy:** [Micbooster](https://micbooster.com/product/clippy-ultra-xlr/) (manufacturer, UK / worldwide shipping) and EU distributors such as [Veldshop](https://www.veldshop.nl/en/)
- **Approx. price:** ~£70 (Micbooster, incl. UK VAT) / ~€92 (Veldshop, incl. 21% EU VAT) for a single microphone; stereo matched pairs ~£148
- **Frequency response:** 20 Hz to 110 kHz
- **Polar pattern:** Omnidirectional in the audible range, becoming progressively more directional above 20 kHz
- **Power and connector:** 12 V to 48 V phantom power via standard male XLR (1.5 m shielded cable)
- **Specs:** MEMS ultrasonic capsule, -40 dB sensitivity @ 1 kHz, 117 dB max SPL

The Clippy Ultra XLR is a compact, field-proven microphone covering both audible bird song and ultrasonic bat echolocation up to 110 kHz. Paired with a 192 kHz XLR audio interface, it is one of the simplest ways to run BirdNET and BattyBirdNET (or BSG-BAT at 192 kHz) from a single microphone. Compared to standard audible capsules like the Primo EM272, the ultrasonic MEMS capsule has slightly higher self-noise in the audible range, which is a normal trade-off for extended 110 kHz reach.

### Sonorous Objects SO.104 Ultrasonic Omni

- **Where to buy:** Direct from [Sonorous Objects](https://sonorousobjects.nyc/products/so-104-ultrasonic-omni-microphone) (US / worldwide shipping)
- **Approx. price:** ~$90 USD (starting price for a single microphone)
- **Frequency response:** 20 Hz to 70 kHz
- **Polar pattern:** Omnidirectional
- **Power and connector:** Standard 48 V phantom power (IEC 61938), integrated male 3-pin Neutrik XLR
- **Specs:** Primo EM258 capsule, 74 dB SNR, 20 dBA self-noise, 115 dB max SPL, -32 dB sensitivity (+/-3 dB @ 1 kHz), balanced transformerless output

The SO.104 houses a Primo EM258 ultrasonic-capable capsule inside a standard 20 mm die-cast zinc pencil-mic body, so it fits common mic clips and shock mounts and connects with any standard XLR cable. Its high sensitivity and low self-noise make it strong for combined bird and bat monitoring. Note that its rated response extends to 70 kHz, which covers many common bat species well, though very high-frequency species (above 70 to 80 kHz) will be more attenuated than on 110+ kHz MEMS microphones.

### Sonorous Objects SO.4 Ultrasonic Omni

- **Where to buy:** Direct from [Sonorous Objects](https://sonorousobjects.nyc/products/so-4-ultrasonic-omni-microphone) (US / worldwide shipping)
- **Approx. price:** ~$110 USD (starting price; varies by connector option and single vs. matched pair)
- **Frequency response:** 20 Hz to 70 kHz
- **Polar pattern:** Omnidirectional
- **Power and connector:** Available in 3.5 mm plug-in-power (PIP) or XLR (48 V phantom power via proprietary cable)
- **Specs:** Primo EM258 capsule, 74 dB SNR, 20 dBA self-noise, 115 dB max SPL, -32 dB sensitivity

The SO.4 uses the same Primo EM258 capsule and shares the acoustic specifications of the SO.104 in a much smaller nickel-plated aluminum body (roughly 32 mm long by 13 mm diameter), making it easy to tuck into compact outdoor shelters.

**Important XLR cable warning:** If you buy the XLR version of the SO.4, you **must** use the included Sonorous Objects XLR cable. That cable contains the step-down circuitry that converts 48 V phantom power safely for the capsule; standard third-party XLR cables are not compatible and can damage the microphone. If you want standard replaceable XLR cabling, choose the [SO.104](#sonorous-objects-so104-ultrasonic-omni) instead.

### Rode NT5 Studio Microphone

- **Where to buy:** Major music and pro-audio retailers worldwide (such as Thomann, Sweetwater, B&H, and local music stores; look for the single **NT5-S** unit rather than the stereo matched pair)
- **Approx. price:** ~$229 USD / ~€189 for a single microphone (matched stereo pairs ~$449 / ~€380 to €420)
- **Product page:** [Rode NT5](https://rode.com/en/microphones/studio-condenser/nt5)
- **Frequency response:** 20 Hz to 20 kHz (official rating); tested into ultrasonic frequencies up to 96 kHz (with a 192 kHz sample rate interface)
- **Polar pattern:** Cardioid (standard 1/2-inch gold-sputtered capsule)
- **Power and connector:** 48 V phantom power, standard male XLR
- **Specs:** 16 dBA self-noise, 143 dB max SPL, -38 dBV sensitivity

Although the Rode NT5 is a studio small-diaphragm condenser officially specified only to 20 kHz, real-world testing has proven that the **Rode NT5 with its standard cardioid capsule reaches ultrasonic frequencies up to 96 kHz** when connected to an audio interface running at a **192 kHz** sample rate. This makes it a viable option if you already own an NT5 or want very low-noise studio audio for birds alongside bat capture.

Keep these limitations in mind:

- **Directionality and roll-off:** A cardioid capsule becomes much more directional at ultrasonic frequencies and rolls off faster above 30 to 40 kHz than a dedicated ultrasonic omnidirectional capsule, so aiming toward bat flight paths matters.
- **Weather sensitivity:** As an indoor studio condenser, the NT5 is not weather-resistant and must be kept dry and sheltered from condensation.
- **Other studio microphones:** Other small-diaphragm studio condenser microphones may also respond above 20 kHz, but they are **not proven**. Do not assume an untested studio microphone will pick up bats without checking a 192 kHz spectrogram first.

## Proven USB Audio Interfaces for XLR Microphones

### Focusrite Scarlett Solo and 2i2 (3rd and 4th Gen)

For XLR phantom-powered microphones, the **Focusrite Scarlett Solo** and **Scarlett 2i2** (**3rd Generation** and **4th Generation**) are proven, reliable, and solid options that work well with both **Raspberry Pi** and **PC** hosts running BirdNET-Go.

- **Where to buy:** Direct from [Focusrite](https://focusrite.com/scarlett) and major music/pro-audio retailers worldwide (Thomann, Sweetwater, B&H, Amazon, and local music stores; 3rd Gen units are also widely available refurbished or used)
- **Approx. price:**
  - **Scarlett Solo:** ~$100 / ~€100 to €110 (3rd Gen; ~$65 to $80 used/refurbished), ~$140 to $150 / ~€135 to €150 (4th Gen)
  - **Scarlett 2i2:** ~$170 / ~€150 to €170 (3rd Gen; ~$75 to $90 used/refurbished), ~$200 to $220 / ~€190 to €215 (4th Gen)
- **Sample rate:** Up to **192 kHz** (96 kHz Nyquist), which is the recommended rate for BattyBirdNET and meets the 192 kHz minimum for BSG-BAT.
- **Phantom power and preamps:** Clean, low-noise preamplifiers with switchable **+48 V phantom power**.
- **Linux compatibility:** Class-compliant USB audio under ALSA with no proprietary drivers needed; modern Linux kernels also expose hardware controls (such as phantom power and Air mode) via `alsamixer`.
- **Raspberry Pi power tips:**
  - Use the official Raspberry Pi power supply so +48 V phantom power does not cause USB voltage drops.
  - On the Scarlett 2i2 4th Gen, you can use the auxiliary 5 V USB-C power port if your host's USB port has limited current; on bus-powered models, a quality externally powered USB hub solves dropouts if they occur.
  - Use short USB cables (under 2 m) between the host and the interface, and use balanced XLR cable for any longer run to the microphone.

Note that 192 kHz interfaces cannot capture at 384 kHz. If you want full 384 kHz capture for BSG-BAT or very high-frequency bat species (above ~95 kHz), choose a 384 kHz USB ultrasonic microphone instead.

## Weatherproofing and Outdoor Mounting

Most ultrasonic microphones, especially MEMS-based models (AudioMoth, Dodotronic UltraMic, Vleermic, Pippyg, Griff/Griff Mini, and Clippy Ultra), are sensitive to **moisture, condensation, and water ingress**. Even small droplets or overnight dew entering the tiny acoustic port of a MEMS capsule can temporarily block ultrasonic pickup, raise the noise floor, or corrode and permanently damage the sensor.

- **Orient the microphone capsule pointing downward:** Always mount the microphone so the capsule or acoustic port faces **straight down**. Gravity keeps falling rain, dripping water, and dust out of the port while omnidirectional ultrasonic capsules still pick up overhead bat calls well. Never point an unprotected capsule upward into the open sky.
- **Shield from rain and splashback:** Mount the microphone under roof eaves, inside a downward-facing rain hood, or inside a weatherproof housing with only the acoustic port exposed on the underside. Keep it high enough above hard surfaces so heavy rain does not splash upward into the port.
- **Prevent water from tracking down cables:** Add a **drip loop** (a U-shaped bend below the connector or enclosure entry) on USB and XLR cables so rainwater drips off the bottom of the loop instead of running along the cable into the microphone or USB connector.
- **Manage condensation and humidity:** Outdoor temperature swings from warm evenings to cool nights easily cause condensation inside unventilated boxes and on cold metal bodies. Avoid sealing electronics inside completely airtight bags or boxes without desiccant or a breathable vent, and keep bare-PCB microphones (AudioMoth USB, Vleermic, Pippyg, Griff/Griff Mini) sheltered from direct dew and fog.
- **Factory waterproof membrane options:** Some microphones, such as the **Dodotronic UltraMic 384K PRO**, can be ordered with a protective acoustic membrane (**With Waterproof Membrane: essential for harsh, humid, or outdoor environments**). Even with a waterproof membrane, mount the microphone with the sensor facing downward and shield it from direct driving rain. **Note:** The protective membrane introduces a reduction in sensitivity at the highest frequency ranges (AudioMoth acoustic membrane cases have the same trade-off).
- **Avoid windscreens that block ultrasound:** Avoid thick foam windscreens, heavy cloth, or plastic wrap over ultrasonic capsules. They strongly attenuate ultrasonic frequencies and can hold moisture directly against the sensor. If insects or spiders are a problem, use a breathable mesh spaced slightly away from the capsule so water cannot bridge across the mesh onto the port.

## Compatibility and Setup Tips

- **Avoid proprietary mobile-only dongles:** Some handheld ultrasonic microphones (such as the Wildlife Acoustics Echo Meter Touch 2) rely on proprietary mobile apps and protocols rather than standard USB Audio Class drivers, so they do not expose a standard Linux ALSA capture device for BirdNET-Go. Look for explicit **UAC1** or **UAC2** class-compliant support.
- **Use mono cleanly on multi-channel interfaces:** On two-input interfaces like the Scarlett 2i2, BirdNET-Go downmixes stereo inputs to mono unless a single channel is selected. Keep unused inputs turned all the way down (or configure channel mode) so an empty preamp does not add background noise.
- **Verify real ultrasonic capture:** After configuring the source and sample rate (96, 192, 256, or 384 kHz) in **Settings > Audio**, inspect a bat detection spectrogram or check the startup logs to confirm ALSA opened the device at the requested hardware rate without falling back to 48 kHz. Bat spectrograms scale to half the clip sample rate (for example, 0 to 96 kHz at 192 kHz, and 0 to 192 kHz at 384 kHz).
