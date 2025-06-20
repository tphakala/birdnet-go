#!/bin/bash
# BirdNET-Go VM Image Build Script
# Usage: ./build.sh [amd64|arm64] [version]

set -e

# Function to show help
show_help() {
    cat << 'EOF'
🐦 BirdNET-Go VM Image Builder

USAGE:
    ./build.sh [ARCHITECTURE] [VERSION]
    ./build.sh --help

ARGUMENTS:
    ARCHITECTURE    Target architecture (amd64 or arm64) [default: amd64]
    VERSION         Version string for the image [default: dev-YYYYMMDD]

OPTIONS:
    --help, -h      Show this help message

EXAMPLES:
    ./build.sh                    # Build amd64 with default version
    ./build.sh arm64              # Build arm64 with default version  
    ./build.sh amd64 v1.0.0       # Build amd64 with specific version

REQUIREMENTS:
    - packer
    - qemu-system-x86_64 (for amd64)
    - qemu-system-aarch64 (for arm64)
    - zstd

The script will create a compressed qcow2 VM image with BirdNET-Go pre-installed.
EOF
    exit 0
}

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Function to print colored messages
print_message() {
    echo -e "${2}${1}${NC}"
}

# Check for help option
if [[ "$1" == "--help" || "$1" == "-h" ]]; then
    show_help
fi

# Default values
ARCH="${1:-amd64}"
VERSION="${2:-dev-$(date +%Y%m%d)}"
OUTPUT_DIR="output"

print_message "🐦 BirdNET-Go VM Image Builder" "$GREEN"
print_message "Architecture: $ARCH" "$YELLOW"
print_message "Version: $VERSION" "$YELLOW"

# Validate architecture
if [[ "$ARCH" != "amd64" && "$ARCH" != "arm64" ]]; then
    print_message "❌ Invalid architecture. Use 'amd64' or 'arm64'" "$RED"
    exit 1
fi

# Check dependencies
print_message "🔍 Checking dependencies..." "$YELLOW"

MISSING_DEPS=()

if ! command -v packer &> /dev/null; then
    MISSING_DEPS+=("packer")
fi

if ! command -v qemu-system-x86_64 &> /dev/null && [ "$ARCH" = "amd64" ]; then
    MISSING_DEPS+=("qemu-system-x86")
fi

if ! command -v qemu-system-aarch64 &> /dev/null && [ "$ARCH" = "arm64" ]; then
    MISSING_DEPS+=("qemu-system-arm")
fi

if ! command -v zstd &> /dev/null; then
    MISSING_DEPS+=("zstd")
fi

if [ ${#MISSING_DEPS[@]} -ne 0 ]; then
    print_message "❌ Missing dependencies: ${MISSING_DEPS[*]}" "$RED"
    print_message "Install them with your package manager:" "$YELLOW"
    print_message "  Ubuntu/Debian: sudo apt-get install packer qemu-system qemu-utils zstd" "$YELLOW"
    print_message "  macOS: brew install packer qemu zstd" "$YELLOW"
    exit 1
fi

print_message "✅ All dependencies found" "$GREEN"

# Create necessary directories
mkdir -p {templates,scripts,files,$OUTPUT_DIR}

# Generate SSH keys for build if they don't exist
if [ ! -f "build_key" ]; then
    print_message "🔑 Generating SSH keys for build..." "$YELLOW"
    ssh-keygen -t rsa -b 4096 -f build_key -N "" -C "birdnet-go-build-$ARCH"
    chmod 600 build_key
    chmod 644 build_key.pub
fi

# Create cloud-init templates if they don't exist
if [ ! -f "templates/meta-data.yml" ]; then
    print_message "📝 Creating cloud-init templates..." "$YELLOW"
    
    cat > templates/meta-data.yml << 'EOF'
instance-id: birdnet-go-vm
local-hostname: ${hostname}
EOF

    cat > templates/user-data.yml << 'EOF'
#cloud-config
users:
  - name: birdnet
    groups: sudo, docker, audio, adm
    shell: /bin/bash
    sudo: ALL=(ALL) NOPASSWD:ALL
    ssh_authorized_keys:
      - ${ssh_public_key}

# Disable password authentication
ssh_pwauth: false

# Set timezone
timezone: UTC

# Network configuration
network:
  version: 2
  ethernets:
    eth0:
      dhcp4: true

# Resize root filesystem
growpart:
  mode: auto
  devices: ['/']

# Package updates
package_update: true
package_upgrade: true

# Install essential packages
packages:
  - curl
  - wget
  - git
  - htop
  - nano
  - vim
  - unattended-upgrades

# Write files
write_files:
  - path: /etc/birdnet-go/version
    content: |
      Version: ${version}
      Architecture: ${arch}
      Build Date: $(date -u +%Y-%m-%dT%H:%M:%SZ)
    owner: root:root
    permissions: '0644'

# Enable automatic security updates
runcmd:
  - systemctl enable unattended-upgrades
  - systemctl start unattended-upgrades
  - mkdir -p /etc/birdnet-go
  - mkdir -p /opt/birdnet-go/{config,data}
  - chown -R birdnet:birdnet /opt/birdnet-go

# Final message
final_message: "BirdNET-Go VM is ready! Version: ${version}, Architecture: ${arch}"
EOF
fi

# Create setup scripts if they don't exist
if [ ! -f "scripts/setup-birdnet-go.sh" ]; then
    print_message "📝 Creating setup scripts..." "$YELLOW"
    
    cat > scripts/setup-birdnet-go.sh << 'EOF'
#!/bin/bash
set -e

echo "Setting up BirdNET-Go..."

# Create directories
sudo mkdir -p /opt/birdnet-go/{config,data,scripts}
sudo chown -R birdnet:birdnet /opt/birdnet-go

# Download base configuration
curl -s https://raw.githubusercontent.com/tphakala/birdnet-go/main/internal/conf/config.yaml \
  -o /opt/birdnet-go/config/config.yaml

# Set absolute path for audio clips
sed -i 's|path: clips/|path: /opt/birdnet-go/data/clips/|' /opt/birdnet-go/config/config.yaml

# Enable XNNPACK for performance
sed -i 's/usexnnpack: false/usexnnpack: true/' /opt/birdnet-go/config/config.yaml

# Create clips directory
mkdir -p /opt/birdnet-go/data/clips

# Create systemd service
sudo tee /etc/systemd/system/birdnet-go.service > /dev/null << 'SERVICE_EOF'
[Unit]
Description=BirdNET-Go
After=docker.service
Requires=docker.service

[Service]
Type=simple
User=birdnet
Group=birdnet
Restart=always
RestartSec=10

# Pull latest image
ExecStartPre=/usr/bin/docker pull tphakala/birdnet-go:nightly

# Remove any existing container
ExecStartPre=-/usr/bin/docker rm -f birdnet-go

# Run BirdNET-Go
ExecStart=/usr/bin/docker run --rm \
  --name birdnet-go \
  -p 8080:8080 \
  --device /dev/snd \
  -v /opt/birdnet-go/config:/config \
  -v /opt/birdnet-go/data:/data \
  -e TZ=UTC \
  -e BIRDNET_UID=1000 \
  -e BIRDNET_GID=1000 \
  tphakala/birdnet-go:nightly

# Cleanup
ExecStop=-/usr/bin/docker stop birdnet-go
ExecStopPost=-/usr/bin/docker rm -f birdnet-go

[Install]
WantedBy=multi-user.target
SERVICE_EOF

# Enable service
sudo systemctl daemon-reload
sudo systemctl enable birdnet-go.service

# Pull Docker image
docker pull tphakala/birdnet-go:nightly

echo "BirdNET-Go setup completed"
EOF

    cat > scripts/configure-services.sh << 'EOF'
#!/bin/bash
set -e

echo "Configuring system services..."

# Configure firewall
sudo ufw --force enable
sudo ufw allow ssh
sudo ufw allow 8080/tcp

# Configure automatic updates
sudo systemctl enable unattended-upgrades

# Configure log rotation
sudo tee /etc/logrotate.d/birdnet-go > /dev/null << 'LOGROTATE_EOF'
/opt/birdnet-go/data/logs/*.log {
  daily
  rotate 7
  compress
  delaycompress
  missingok
  notifempty
  create 0644 birdnet birdnet
  postrotate
    systemctl reload birdnet-go || true
  endscript
}
LOGROTATE_EOF

# Create update script
sudo tee /usr/local/bin/update-birdnet-go > /dev/null << 'UPDATE_EOF'
#!/bin/bash
echo "Updating BirdNET-Go..."
systemctl stop birdnet-go
docker pull tphakala/birdnet-go:nightly
systemctl start birdnet-go
echo "Update completed"
UPDATE_EOF

sudo chmod +x /usr/local/bin/update-birdnet-go

# Create weekly update cron job
echo "0 2 * * 0 /usr/local/bin/update-birdnet-go" | sudo tee /etc/cron.d/birdnet-go-update

echo "Service configuration completed"
EOF

    cat > scripts/cleanup.sh << 'EOF'
#!/bin/bash
set -e

echo "Cleaning up system..."

# Remove build packages
sudo apt-get autoremove -y
sudo apt-get autoclean

# Clear package cache
sudo apt-get clean

# Clear temporary files
sudo rm -rf /tmp/*
sudo rm -rf /var/tmp/*

# Clear logs
sudo journalctl --vacuum-time=1d

# Clear bash history
history -c
rm -f ~/.bash_history

# Clear cloud-init logs and cache
sudo cloud-init clean --logs

# Clear SSH host keys (will be regenerated on first boot)
sudo rm -f /etc/ssh/ssh_host_*

# Clear machine ID (will be regenerated)
sudo truncate -s 0 /etc/machine-id

# Zero out free space for better compression
sudo dd if=/dev/zero of=/EMPTY bs=1M || true
sudo rm -f /EMPTY

echo "Cleanup completed"
EOF

    chmod +x scripts/*.sh
fi

# Initialize Packer if needed
if [ ! -f ".packer.d" ]; then
    print_message "🔧 Initializing Packer..." "$YELLOW"
    packer init birdnet-go-vm.pkr.hcl
fi

# Validate Packer configuration
print_message "🔍 Validating Packer configuration..." "$YELLOW"
SSH_PUBLIC_KEY=$(cat build_key.pub)
packer validate \
    -var "version=$VERSION" \
    -var "arch=$ARCH" \
    -var "output_dir=$OUTPUT_DIR" \
    -var "ssh_public_key=$SSH_PUBLIC_KEY" \
    -var "ssh_private_key_file=build_key" \
    -var "use_kvm=true" \
    birdnet-go-vm.pkr.hcl

# Build the image
print_message "🏗️ Building VM image..." "$GREEN"
print_message "This may take 10-30 minutes depending on your system..." "$YELLOW"

SSH_PUBLIC_KEY=$(cat build_key.pub)
packer build \
    -var "version=$VERSION" \
    -var "arch=$ARCH" \
    -var "output_dir=$OUTPUT_DIR" \
    -var "ssh_public_key=$SSH_PUBLIC_KEY" \
    -var "ssh_private_key_file=build_key" \
    -var "use_kvm=true" \
    birdnet-go-vm.pkr.hcl

# Show results
print_message "✅ Build completed!" "$GREEN"
print_message "📦 Build artifacts:" "$YELLOW"

for file in $OUTPUT_DIR/*; do
    if [ -f "$file" ]; then
        size=$(du -h "$file" | cut -f1)
        print_message "  $(basename "$file"): $size" "$NC"
    fi
done

print_message "🚀 Your VM image is ready!" "$GREEN"
print_message "💡 Quick start:" "$YELLOW"
print_message "  1. Decompress: zstd -d $OUTPUT_DIR/*.qcow2.zst" "$NC"
print_message "  2. Verify: sha256sum -c $OUTPUT_DIR/*.sha256" "$NC"
print_message "  3. Run with QEMU, Proxmox, or your preferred virtualization platform" "$NC" 