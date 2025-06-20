# BirdNET-Go KVM/VM Images

This directory contains automation for building pre-configured KVM virtual machine images with BirdNET-Go installed and ready to use.

## Overview

Pre-built VM images provide an alternative deployment method for users who prefer virtual machines over Docker containers. These images come with BirdNET-Go pre-installed, configured, and ready to run.

## Use Cases

### **Perfect For:**
- **Proxmox Users**: Import qcow2 images directly into Proxmox VE
- **libvirt/KVM**: Native support for qcow2 format
- **QEMU Users**: Direct qcow2 compatibility
- **Virtualization Platforms**: VMware (with conversion), VirtualBox (with conversion)
- **Cloud Deployments**: Custom images for cloud platforms
- **Air-Gapped Systems**: Pre-built images for isolated networks
- **Education/Training**: Ready-to-use classroom environments
- **Development**: Consistent testing environments

### **Advantages:**
- ✅ **Zero Configuration**: Boot and run immediately
- ✅ **Isolated Environment**: Complete OS isolation
- ✅ **Resource Control**: Dedicated CPU, RAM, and storage
- ✅ **Snapshot Support**: Easy backup and rollback
- ✅ **Network Flexibility**: Bridge, NAT, or host networking
- ✅ **Security**: Full OS-level security features

## Architecture Support

### **Supported Architectures:**
- **x86_64 (amd64)**: Intel/AMD 64-bit processors
- **aarch64 (arm64)**: ARM 64-bit processors (Apple M1/M2, ARM servers)

### **Image Specifications:**
- **Base OS**: Ubuntu 22.04 LTS Server (minimal)
- **Image Format**: qcow2 (QEMU Copy-On-Write)
- **Compression**: zstd compression for smaller downloads
- **Size**: ~2GB compressed, ~8GB expanded
- **Filesystem**: ext4 with automatic resize on first boot

## Pre-Installed Components

### **System Components:**
- Ubuntu 22.04 LTS Server (latest updates)
- Docker and Docker Compose
- BirdNET-Go (latest nightly) via Docker
- Systemd service configuration
- SSH server (key-based authentication)
- Cloud-init for initial configuration
- QEMU Guest Agent
- Basic monitoring tools (htop, iotop, etc.)

### **BirdNET-Go Configuration:**
- Web interface on port 8080
- Audio device auto-detection
- Log rotation configured
- Automatic updates enabled
- Performance optimizations applied

## Build Automation

### **GitHub Actions Workflow:**
```yaml
# Triggered on:
- Release tags (v*.*.*)
- Manual workflow dispatch
- Monthly automated builds
- Pull requests (build only, no upload)
```

### **Build Matrix:**
```yaml
strategy:
  matrix:
    arch: [amd64, arm64]
    base: [ubuntu-22.04]
```

### **Build Process:**
1. **Prepare Build Environment**: Install Packer, QEMU, dependencies
2. **Download Base Image**: Ubuntu Server cloud image
3. **Customize with Packer**: Install BirdNET-Go and dependencies
4. **Optimize Image**: Remove unnecessary packages, clean caches
5. **Compress Image**: zstd compression for distribution
6. **Generate Checksums**: SHA256 checksums for verification
7. **Upload to Releases**: Attach to GitHub release

## Download and Usage

### **Available Downloads:**
```
birdnet-go-vm-amd64-v1.0.0.qcow2.zst      # x86_64 compressed image
birdnet-go-vm-amd64-v1.0.0.qcow2.zst.sha256   # Checksum
birdnet-go-vm-arm64-v1.0.0.qcow2.zst      # ARM64 compressed image  
birdnet-go-vm-arm64-v1.0.0.qcow2.zst.sha256   # Checksum
```

### **Quick Start:**
```bash
# Download and verify
wget https://github.com/tphakala/birdnet-go/releases/latest/download/birdnet-go-vm-amd64-v1.0.0.qcow2.zst
wget https://github.com/tphakala/birdnet-go/releases/latest/download/birdnet-go-vm-amd64-v1.0.0.qcow2.zst.sha256
sha256sum -c birdnet-go-vm-amd64-v1.0.0.qcow2.zst.sha256

# Decompress
zstd -d birdnet-go-vm-amd64-v1.0.0.qcow2.zst

# Run with QEMU/KVM
qemu-system-x86_64 \
  -enable-kvm \
  -m 2G \
  -cpu host \
  -netdev user,id=net0,hostfwd=tcp::8080-:8080 \
  -device virtio-net-pci,netdev=net0 \
  -drive file=birdnet-go-vm-amd64-v1.0.0.qcow2,format=qcow2 \
  -device virtio-rng-pci
```

## Platform-Specific Guides

### **Proxmox VE:**
```bash
# Import image to Proxmox
qm create 100 --name birdnet-go --memory 2048 --net0 virtio,bridge=vmbr0
qm importdisk 100 birdnet-go-vm-amd64-v1.0.0.qcow2 local-lvm
qm set 100 --scsihw virtio-scsi-pci --scsi0 local-lvm:vm-100-disk-0
qm set 100 --boot c --bootdisk scsi0
qm set 100 --agent enabled=1
qm start 100
```

### **libvirt/virt-manager:**
```bash
# Create VM with virt-install
virt-install \
  --name birdnet-go \
  --ram 2048 \
  --vcpus 2 \
  --disk path=birdnet-go-vm-amd64-v1.0.0.qcow2,format=qcow2 \
  --network bridge=virbr0 \
  --graphics none \
  --console pty,target_type=serial \
  --import
```

### **VMware Workstation/ESXi:**
```bash
# Convert qcow2 to vmdk
qemu-img convert -f qcow2 -O vmdk birdnet-go-vm-amd64-v1.0.0.qcow2 birdnet-go.vmdk
# Import vmdk into VMware
```

## First Boot Configuration

### **Cloud-init Integration:**
The VM images use cloud-init for first-boot configuration:

```yaml
# /var/lib/cloud/seed/nocloud/user-data
#cloud-config
users:
  - name: birdnet
    sudo: ALL=(ALL) NOPASSWD:ALL
    shell: /bin/bash
    ssh_authorized_keys:
      - ssh-rsa AAAAB3NzaC1y... # Your SSH public key

# Set timezone
timezone: UTC

# Network configuration
network:
  version: 2
  ethernets:
    eth0:
      dhcp4: true

# Run configuration script
runcmd:
  - /opt/birdnet-go/first-boot-setup.sh
```

### **Customization Options:**
- SSH key injection
- Network configuration
- Timezone setting
- Initial audio device configuration
- BirdNET-Go settings

## Security Features

### **Default Security:**
- SSH password authentication disabled
- Firewall enabled (UFW)
- Only necessary ports open (22, 8080)
- Regular security updates enabled
- User account with sudo access
- Docker daemon security hardening

### **Recommended Hardening:**
- Change default SSH port
- Set up fail2ban
- Configure log monitoring
- Enable automatic security updates
- Use VPN for remote access

## Performance Optimization

### **Resource Recommendations:**
- **Minimum**: 1 CPU, 1GB RAM, 8GB storage
- **Recommended**: 2 CPU, 2GB RAM, 20GB storage
- **High Activity**: 4 CPU, 4GB RAM, 50GB storage

### **Tuning Options:**
- CPU governor settings
- I/O scheduler optimization
- Memory overcommit handling
- Audio buffer tuning

## Update Strategy

### **Automatic Updates:**
- System packages: Enabled via unattended-upgrades
- BirdNET-Go: Weekly check for new Docker images
- Security updates: Applied automatically

### **Manual Updates:**
```bash
# Update system packages
sudo apt update && sudo apt upgrade -y

# Update BirdNET-Go
sudo systemctl stop birdnet-go
sudo docker pull tphakala/birdnet-go:nightly
sudo systemctl start birdnet-go
```

## Monitoring and Logging

### **Built-in Monitoring:**
- systemd service status monitoring
- Docker container health checks
- Disk space monitoring
- Basic performance metrics

### **Log Locations:**
- BirdNET-Go logs: `/var/log/birdnet-go/`
- System logs: `/var/log/syslog`
- Docker logs: `journalctl -u birdnet-go`

## Backup and Recovery

### **VM Snapshots:**
Most virtualization platforms support snapshots for easy backup/restore.

### **Data Backup:**
```bash
# Backup BirdNET-Go data
sudo tar -czf birdnet-go-backup-$(date +%Y%m%d).tar.gz \
  /opt/birdnet-go/data \
  /opt/birdnet-go/config
```

### **Recovery Process:**
1. Deploy new VM from image
2. Restore configuration and data
3. Restart services

## Development and Customization

### **Build Your Own Images:**
```bash
# Clone repository
git clone https://github.com/tphakala/birdnet-go.git
cd birdnet-go/vm-images

# Build custom image
packer build -var 'version=custom' birdnet-go-vm.pkr.hcl
```

### **Customization Options:**
- Different base OS (Debian, CentOS)
- Additional software packages
- Custom BirdNET-Go configuration
- Enterprise authentication integration

## Troubleshooting

### **Common Issues:**
- Audio device not detected: Check USB passthrough
- Network connectivity: Verify bridge/NAT configuration
- Performance issues: Increase CPU/RAM allocation
- Boot failures: Check virtualization settings

### **Getting Help:**
- GitHub Issues: Technical problems
- Discussions: Usage questions
- Wiki: Detailed documentation

## Future Enhancements

### **Planned Features:**
- OVA/OVF format support
- Hyper-V VHDX images
- Cloud platform images (AWS AMI, GCP, Azure)
- Kubernetes/container runtime options
- High availability configurations
- GPU acceleration support

## Contributing

Contributions welcome for:
- Platform-specific guides
- Performance optimizations
- Security improvements
- Additional architectures
- Documentation updates 