---
name: Bug report / Feature request
about: Create a report to help us improve or suggest an idea for this project
title: ''
labels: ''
assignees: ''

---

## Issue Type
- [ ] Bug report
- [ ] Feature request
- [ ] Question / Support

## Description
[A clear and concise description of the issue or feature request]

## Steps to Reproduce (for bugs)
1. [First Step]
2. [Second Step]
3. [and so on...]

## Expected behavior
[What you expected to happen]

## Actual behavior
[What actually happened]

## Screenshots
[If applicable, add screenshots to help explain your problem]

## Environment
### Version Information (build date)
Provide the following line from your system:
```
$ ./birdnet-go
[Paste the output line here, which should look like this:]
BirdNET-Go build date: YYYY-MM-DDTHH:MM:SSZ, using config file: /path/to/config.yaml
```

### System Information
- Device: 
  - [ ] Raspberry Pi (specify model, e.g., Raspberry Pi 4 Model B)
  - [ ] Other SBC (please specify)
  - [ ] PC/Server (please specify)
- OS: [e.g. Raspberry Pi OS, Ubuntu, Debian]
- OS Version: [e.g. Raspberry Pi OS Bullseye, Ubuntu 20.04.2 LTS]
- Hardware specs:
  - CPU: [e.g. Raspberry Pi 4 Broadcom BCM2711, quad-core Cortex-A72 (ARM v8) 64-bit SoC @ 1.5GHz]
  - RAM: [e.g. 4GB]
  - Storage type and size: [e.g. 32GB microSD card, 128GB SSD]
- Additional hardware (if applicable):
  - USB Audio device: [e.g. Brand, Model]
  - Other peripherals: [e.g. Specific microphone, GPS module]

### For Non-Raspberry Pi Linux Users
Have you tested with the latest dev branch container image?
- [ ] Yes
- [ ] No
- [ ] Not applicable (Windows/macOS user)

If not, first test with the latest dev branch container image and see if the issue is already fixed.
```
$ docker pull ghcr.io/tphakala/birdnet-go:dev
```

## Additional context
[Add any other context about the problem here, such as any custom configurations or specific usage scenarios]

## Possible Solution
[If you have a suggestion for fixing the bug or implementing the feature]