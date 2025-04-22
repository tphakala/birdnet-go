# BirdNET-Go Hardware Recommendations

This document outlines the recommended hardware configurations for running BirdNET-Go effectively, ensuring optimal performance for both detection capabilities and user interface responsiveness.

## Compute Hardware

### Recommended Systems

* **Primary Recommendation:** Raspberry Pi 5 (2GB RAM model is sufficient)
  * **Rationale:** While BirdNET-Go's core real-time detection can run adequately on older models, the Pi 5 offers significantly improved performance particularly for the web interface, resulting in much more responsive spectrogram rendering and overall experience.
  * **Processing Speed:** The Pi 5 processes audio chunks significantly faster than older models, allowing for more reliable Deep Detection when using high overlap settings.

* **Alternative Systems:**
  * **Raspberry Pi 4B (4GB)** - Good performance, especially when overclocked to 1.8GHz
  * **Intel NUC** - Excellent performance, higher power consumption

* **Minimum Viable System:** Raspberry Pi 3B+ (512MB) or Raspberry Pi Zero 2 (512MB)
  * **Note:** While the Pi 3B+ can handle core detection (processing 3-second segments in approximately 500ms), expect slower web interface responsiveness and possible performance issues with Deep Detection and multiple simultaneous RTSP streams.

### RAM Requirements

* **Baseline Usage:** ~250MB for core BirdNET-Go processes
* **Recommended:**
  * 2GB RAM for standard installations (single audio source)
  * Each RTSP stream requires an additional FFmpeg process, which can consume substantial memory, if you have high number of RTSP source streams consider system with 4GB or more RAM

### CPU Considerations

* **Threads:** BirdNET-Go can utilize multiple CPU cores. By default, it automatically optimizes for P-cores on hybrid architectures.
* **Performance Impact:** CPU power directly affects:
  * Maximum achievable overlap values (Deep Detection capability)
  * Spectrogram rendering speed
  * Multiple stream processing capacity
  * Audio export encoding speed

## Operating System

* **Recommended:** Raspberry Pi OS Lite (64-bit) based on Debian Bookworm (Debian 12)
  * **Rationale:** 
    * 64-bit OS is **required** (not optional) because TensorFlow Lite requires a 64-bit system
    * "Lite" version avoids unnecessary desktop components, saving resources
    * Bookworm provides current package support for dependencies

* **Alternative OS Options:**
  * Ubuntu Server 22.04 LTS or newer (64-bit)
  * Debian 12 or newer (64-bit)
  * For desktop systems: Windows 10/11, macOS

## Audio Input Hardware

### Sound Cards

* **Recommended:** USB audio interfaces with the following characteristics:
  * 48kHz sample rate support
  * Low self-noise
  * Proper line/mic input with appropriate gain control
  * Models to consider:
    * CM108 based USB sound card
    * Creative Sound Blaster Play! 3
    * U-Green USB sound card (multiple models)

### Microphones

**Important Note:** BirdNET AI processes all audio as mono only. Always use mono microphones for optimal performance. Stereo microphones can introduce phase errors which may reduce detection accuracy.

#### DIY Microphone (Best Performance)

* **Recommended Capsule:** PUI Audio AOM-5024L-HD-R
  * **Characteristics:** 
    * High sensitivity (-24dB)
    * Low self-noise
    * Omnidirectional pattern
    * Weather-resistant when properly housed

* **Construction Requirements:**
  * **Cable:** Shielded cable with braided copper or aluminum shield
    * Recommend: Mogami or Canare microphone cable
    * Cable length: Keep under 10m to minimize signal degradation
  * **Connector:** Quality metal 3.5mm TRS jack with proper shield connection
  * **Power:** Requires phantom power or bias voltage (can be supplied by many USB interfaces)

#### Pre-made Microphone Options

* ** Lavalier (LAV) Microphones:**
  * Recommended models:
    * **Boya USB BY-LM40** (frequently recommended in the community), in reality not very sensitive meaning audio captured is quite low volume
    * **Clippy EM272 Mono Microphone** - High quality omnidirectional microphone with excellent sensitivity and low self-noise [Available here](https://micbooster.com/clippy-and-pluggy-microphones/65-1084-clippy-em272-microphone.html)
    * Other omnidirectional lavalier microphones

## Storage

* **Primary Storage:** High Endurance MicroSD card
  * **Capacity:** 64GB or larger recommended
  * **Speed Rating:** **V30 rated or higher** to guarantee sufficient write performance
  * **Type Specification:** Look for cards explicitly marketed as:
    * "High Endurance"
    * "Surveillance Grade"
    * "Dashcam Rated"
  * **Recommended Brands:** SanDisk High Endurance, Samsung PRO Endurance, Western Digital Purple SC
  * **Rationale:** Standard MicroSD cards can wear out quickly due to constant read/write operations, leading to system failure. High Endurance cards are designed specifically for 24/7 recording applications.

* **Storage Utilization:**
  * Keep MicroSD card usage below 80% for optimal performance and longevity
  * Consider external USB storage for clip exports if extensive archiving is planned

* **Alternative Storage Options:**
  * **SSD Boot (Raspberry Pi 4/5):** Boot from USB SSD for improved reliability and performance
    * Requires Raspberry Pi OS 2021-01-11 or newer
    * Significantly improves system responsiveness and storage reliability
  * **NAS Storage:** Configure clip export to a network share for centralized storage
    * Requires stable network connection
    * Reduces wear on local storage

## Networking and Connectivity

* **Wired Ethernet Recommended:**
  * More reliable than Wi-Fi for 24/7 deployment
  * Required for maximum web interface performance, especially when viewing spectrograms
  * Essential for reliable RTSP stream ingestion

* **Wi-Fi Considerations:**
  * Use 5GHz networks when possible (less interference)
  * Position for strong signal (> -65dBm) if relying on Wi-Fi
  * Not recommended for critical deployments or multiple RTSP streams

* **Bandwidth Requirements:**
  * Basic web interface usage: ~1Mbps
  * Live audio streaming via web interface: ~0.5Mbps per stream
  * RTSP ingestion: Dependent on stream quality (typically 0.5-4Mbps per stream)
  * BirdWeather uploads: Minimal (~10KB per detection)

## Power Supply Considerations

* **Power Supply - IMPORTANT:**
  * **Only use the official Raspberry Pi power supply** matching your Pi model:
    * Pi 4/5: 5V/3A USB-C official power supply
    * Pi 3: 5V/2.5A micro-USB official power supply
  * **Rationale for official PSU requirement:**
    * Official Pi PSUs deliver 5.1V (instead of standard 5V) to compensate for voltage drop
    * Better EMI/RFI shielding than third-party options, reducing audio interference
    * Properly rated for the current demands of the Pi under load
    * Using non-official PSUs may lead to under-voltage warnings and unstable operation

* **Power Consumption Estimates:**
  * Raspberry Pi 3B+: ~3W (idle) to ~5.5W (active analysis)
  * Raspberry Pi 4B: ~4W (idle) to ~6.5W (active analysis)
  * Raspberry Pi 5: ~5W (idle) to ~8W (active analysis)
  * Add ~0.5-1W for USB audio interface and microphone

## Performance Optimization

### Hardware Optimizations

* **Raspberry Pi Specific:**
  * **Overclocking:** For Pi 4, consider modest overclocking to 1.8GHz for improved performance
    * Add to `/boot/config.txt`:
      ```
      over_voltage=2
      arm_freq=1800
      ```
  * **Memory Split:** Allocate minimum GPU memory (64MB) for headless installations
    * Add to `/boot/config.txt`: 
      ```
      gpu_mem=64
      ```

* **Storage Optimizations:**
  * Consider disabling swap for SD card installations to reduce wear
  * For advanced users, move log files to RAM disk

### Software Configurations for Hardware Constraints

* **Limited CPU Resources:**
  * Reduce `birdnet.overlap` value (set to 0.0 for minimal CPU usage)
  * Limit the number of RTSP streams
  * Consider using `birdnet.threads` setting to reserve CPU resources for other tasks

* **Limited Memory Resources:**
  * Disable thumbnail generation in the web interface
  * Use WAV format for audio exports (requires less processing)
  * Implement aggressive retention policies for audio clips

## Additional Resources

* **Community Recommendations:**
  * Join the [BirdNET-Go GitHub Discussions](https://github.com/tphakala/birdnet-go/discussions) for user experiences with different hardware setups
  * The [BirdWeather Community](https://www.birdweather.com/) offers additional insights on microphone selection and placement

* **Testing Your Setup:**
  * Use the built-in `benchmark` command to evaluate system performance:
    ```bash
    birdnet benchmark
    ```
  * Test microphone sensitivity with known bird calls at measured distances
  * Compare detection rates across different hardware configurations

---

*This document represents recommended configurations based on community experience. Individual requirements may vary based on specific monitoring goals, environmental conditions, and budget constraints.*