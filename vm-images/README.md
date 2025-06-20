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

## 🏗️ Architecture

The VM uses a **two-disk architecture** for optimal data persistence and easy updates:

### Main OS Disk (8GB)
- **Purpose**: Operating system, applications, and configuration
- **Contents**: Ubuntu 24.10, Docker, BirdNET-Go configuration
- **Replaceable**: Can be updated/replaced without data loss
- **Mount**: Root filesystem (`/`)

### Data Disk (Separate)
- **Purpose**: Persistent data storage
- **Contents**: Audio clips, SQLite database, logs, backups
- **Persistent**: Survives VM updates and rebuilds
- **Mount**: `/data/birdnet-go/`
- **Recommended Size**: 50GB+ (depends on retention needs)

## 🚀 Quick Start

### 1. Download VM Image

Download the latest compressed VM image for your architecture:

```bash
# AMD64
wget https://github.com/tphakala/birdnet-go/releases/download/v1.x.x/birdnet-go-vm-amd64-v1.x.x.qcow2.zst

# ARM64  
wget https://github.com/tphakala/birdnet-go/releases/download/v1.x.x/birdnet-go-vm-arm64-v1.x.x.qcow2.zst
```

### 2. Extract Image

```bash
zstd -d birdnet-go-vm-amd64-v1.x.x.qcow2.zst
```

### 3. Create Data Disk

Create a separate disk for persistent data:

```bash
# Create a 50GB data disk
qemu-img create -f qcow2 birdnet-go-data.qcow2 50G
```

### 4. Start VM

#### QEMU/KVM
```bash
qemu-system-x86_64 \
  -enable-kvm \
  -m 2048 \
  -smp 2 \
  -drive file=birdnet-go-vm-amd64-v1.x.x.qcow2,format=qcow2 \
  -drive file=birdnet-go-data.qcow2,format=qcow2 \
  -netdev user,id=net0,hostfwd=tcp::8080-:8080 \
  -device virtio-net,netdev=net0 \
  -nographic
```

#### Proxmox VE
1. Upload both disk images to Proxmox storage
2. Create new VM with:
   - **Disk 1**: Main OS disk (birdnet-go-vm-xxx.qcow2)
   - **Disk 2**: Data disk (birdnet-go-data.qcow2)
   - **Network**: Bridge with port 8080 accessible
3. Start VM

#### VMware vSphere/ESXi
1. Convert qcow2 to VMDK:
   ```bash
   qemu-img convert -f qcow2 -O vmdk birdnet-go-vm-amd64-v1.x.x.qcow2 birdnet-go-vm.vmdk
   qemu-img convert -f qcow2 -O vmdk birdnet-go-data.qcow2 birdnet-go-data.vmdk
   ```
2. Create VM with both disks attached

### 5. Initialize Data Disk

After first boot, initialize the data disk:

```bash
# SSH into the VM (default: user 'birdnet', check cloud-init logs for password)
ssh birdnet@<vm-ip>

# Initialize the data disk (usually /dev/vdb)
sudo /usr/local/bin/init-data-disk /dev/vdb

# Start the data mount service
sudo systemctl start data.mount

# Start BirdNET-Go
sudo systemctl start birdnet-go
```

### 6. Access BirdNET-Go

Open your browser and navigate to: `http://<vm-ip>:8080`

## 📁 Directory Structure

```
/
├── opt/birdnet-go/           # Application files (OS disk)
│   ├── config/               # Configuration files
│   │   └── config.yaml       # Main configuration
│   └── scripts/              # Helper scripts
└── data/birdnet-go/          # Persistent data (Data disk)
    ├── clips/                # Audio recordings
    ├── database/             # SQLite database
    │   └── birdnet.db        # Main database file
    ├── logs/                 # Application logs
    └── backups/              # Database backups
```

## 🔄 Updates & Maintenance

### Updating BirdNET-Go

The two-disk architecture makes updates safe and easy:

1. **Docker Image Updates** (Automatic):
   ```bash
   # Manual update
   sudo systemctl restart birdnet-go
   ```

2. **VM Image Updates** (Major updates):
   - Download new VM image
   - Shut down current VM
   - Replace OS disk with new image
   - Keep existing data disk attached
   - Start VM with new OS disk + existing data disk
   - **All your data is preserved!**

### Backup Strategy

#### Database Backup
```bash
# Create backup
sudo -u birdnet sqlite3 /data/birdnet-go/database/birdnet.db ".backup /data/birdnet-go/backups/birdnet-$(date +%Y%m%d).db"

# Automated daily backup (already configured)
sudo systemctl status birdnet-go-backup.timer
```

#### Full Data Backup
```bash
# Backup entire data disk
sudo rsync -av /data/birdnet-go/ /backup/location/
```

## 🛠️ Advanced Configuration

### Custom Data Disk Size

Resize the data disk if needed:

```bash
# Resize disk file
qemu-img resize birdnet-go-data.qcow2 +20G

# Inside VM, resize filesystem
sudo resize2fs /dev/disk/by-label/birdnet-data
```

### Multiple Data Disks

For large installations, you can use separate disks:

```bash
# Create specialized disks
qemu-img create -f qcow2 birdnet-go-clips.qcow2 100G    # Audio clips
qemu-img create -f qcow2 birdnet-go-database.qcow2 10G  # Database only
```

Update mount configuration in `/etc/systemd/system/` accordingly.

### Network Storage

Mount network storage for data:

```bash
# Example: NFS mount for clips
echo "nfs-server:/path/to/clips /data/birdnet-go/clips nfs defaults 0 0" | sudo tee -a /etc/fstab
```

## 🔧 Troubleshooting

### Data Disk Not Mounting

```bash
# Check disk detection
lsblk

# Check filesystem
sudo fsck /dev/vdb

# Manual mount
sudo mount /dev/disk/by-label/birdnet-data /data
```

### Service Not Starting

```bash
# Check service status
sudo systemctl status birdnet-go
sudo systemctl status data.mount

# Check logs
sudo journalctl -u birdnet-go -f
sudo journalctl -u data.mount -f
```

### Permissions Issues

```bash
# Fix data directory permissions
sudo chown -R 1000:1000 /data/birdnet-go
sudo chmod -R 755 /data/birdnet-go
```

## 📊 System Requirements

- **CPU**: 2+ cores (x86_64 or ARM64)
- **RAM**: 2GB minimum, 4GB recommended
- **Storage**: 
  - OS Disk: 8GB (fixed)
  - Data Disk: 50GB+ recommended (depends on retention)
- **Network**: Port 8080 accessible

## 🔒 Security Features

- Automatic security updates enabled
- UFW firewall configured (SSH + 8080 only)
- Non-root user execution
- Read-only configuration mount
- Separate data disk prevents OS-level data loss

## 🌟 Use Cases

Perfect for:
- **Home bird monitoring** with data persistence
- **Research installations** requiring data integrity
- **Cloud deployments** with separate storage volumes
- **Development/testing** with easy VM replacement
- **Production systems** requiring zero-downtime updates

## 📝 Default Credentials

- **User**: `birdnet`
- **SSH**: Key-based authentication (cloud-init)
- **Web Interface**: No authentication (configure as needed)

## 🏷️ Version Information

- **Base OS**: Ubuntu 24.10 (Oracular Oriole)
- **Docker**: Latest stable
- **BirdNET-Go**: Latest nightly build
- **Architecture**: AMD64 and ARM64 supported 