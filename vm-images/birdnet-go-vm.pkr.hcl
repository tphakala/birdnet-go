packer {
  required_plugins {
    qemu = {
      source  = "github.com/hashicorp/qemu"
      version = "~> 1"
    }
  }
}

# Variables
variable "version" {
  type        = string
  description = "Version tag for the image"
  default     = "nightly"
}

variable "arch" {
  type        = string
  description = "Target architecture (amd64 or arm64)"
  default     = "amd64"
}

variable "base_image" {
  type        = string
  description = "Base Ubuntu 24.10 cloud image URL"
  default     = ""
}

variable "output_dir" {
  type        = string
  description = "Output directory for built images"
  default     = "output"
}

# Local variables for architecture-specific settings
locals {
  base_images = {
    amd64 = "https://cloud-images.ubuntu.com/releases/oracular/release/ubuntu-24.10-server-cloudimg-amd64.img"
    arm64 = "https://cloud-images.ubuntu.com/releases/oracular/release/ubuntu-24.10-server-cloudimg-arm64.img"
  }
  
  base_checksums = {
    amd64 = "sha256:8446856f1903fd305a17cfb610bbb6c01e8e2230cdf41d44fc9e3d824f747ff4"
    arm64 = "sha256:99b858f01e238c74eb263ab8b83ea543f2576cee166e9ed8210c75035526679b"
  }
  
  qemu_machines = {
    amd64 = "pc"
    arm64 = "virt"
  }
  
  qemu_binaries = {
    amd64 = "qemu-system-x86_64"
    arm64 = "qemu-system-aarch64"
  }
  
  qemu_cpus = {
    amd64 = "host"
    arm64 = "host"
  }
  
  qemu_cpus_tcg = {
    amd64 = "qemu64"
    arm64 = "cortex-a57"
  }
  
  accelerators = {
    amd64 = "kvm"
    arm64 = "kvm"
  }
  
  accelerators_no_kvm = {
    amd64 = "tcg"
    arm64 = "tcg"
  }
}

# SSH key variables for build
variable "ssh_public_key" {
  type        = string
  description = "SSH public key for build access"
  default     = ""
}

variable "ssh_private_key_file" {
  type        = string
  description = "Path to SSH private key file"
  default     = ""
}

variable "use_kvm" {
  type        = bool
  description = "Whether to use KVM acceleration (disable for CI environments)"
  default     = true
}

# Build configuration
source "qemu" "birdnet-go" {
  # Image settings
  iso_url      = var.base_image != "" ? var.base_image : local.base_images[var.arch]
  iso_checksum = var.base_image != "" ? "none" : local.base_checksums[var.arch]
  disk_image   = true
  
  # Output settings
  output_directory = "${var.output_dir}-${var.arch}"
  vm_name         = "birdnet-go-vm-${var.arch}-${var.version}.qcow2"
  
  # System settings
  memory          = var.arch == "arm64" && !var.use_kvm ? 3072 : 2048  # More memory for ARM64 TCG
  cpus            = 2
  disk_size       = "8G"
  disk_cache      = "writeback"
  disk_interface  = "virtio"
  net_device      = "virtio-net"
  format          = "qcow2"
  
  # Architecture-specific QEMU settings
  qemu_binary     = local.qemu_binaries[var.arch]
  machine_type    = local.qemu_machines[var.arch]
  cpu_model       = var.use_kvm ? local.qemu_cpus[var.arch] : local.qemu_cpus_tcg[var.arch]
  accelerator     = var.use_kvm ? local.accelerators[var.arch] : local.accelerators_no_kvm[var.arch]
  
  # Headless mode for CI environments
  headless        = !var.use_kvm  # Use headless mode when not using KVM (CI environments)
  vnc_bind_address = var.use_kvm ? "127.0.0.1" : ""  # Disable VNC in CI
  vnc_port_min    = var.use_kvm ? 5900 : 0
  vnc_port_max    = var.use_kvm ? 6000 : 0
  
  # Additional QEMU arguments
  qemuargs = var.arch == "amd64" ? (
    var.use_kvm ? [
      ["-enable-kvm"],
      ["-device", "virtio-rng-pci"]
    ] : [
      ["-device", "virtio-rng-pci"],
      ["-machine", "accel=tcg"],
      ["-display", "none"],
      ["-serial", "stdio"]
    ]
  ) : var.use_kvm ? [
    ["-device", "virtio-rng-pci"]
  ] : [
    ["-device", "virtio-rng-pci"],
    ["-display", "none"],
    ["-serial", "stdio"]
  ]
  
  # Cloud-init settings
  cd_content = {
    "meta-data" = templatefile("${path.root}/templates/meta-data.yml", {
      hostname = "birdnet-go"
    })
    "user-data" = templatefile("${path.root}/templates/user-data.yml", {
      ssh_public_key = var.ssh_public_key
      version       = var.version
      arch          = var.arch
    })
  }
  cd_label = "cidata"
  
  # SSH settings
  ssh_username         = "birdnet"
  ssh_private_key_file = var.ssh_private_key_file != "" ? var.ssh_private_key_file : null
  ssh_password         = var.ssh_private_key_file == "" ? "birdnet-build-temp" : null
  ssh_timeout         = var.use_kvm ? "20m" : "45m"  # TCG builds need more time
  
  # Boot settings
  boot_wait = var.use_kvm ? "10s" : "30s"  # TCG needs more boot time
  
  # Shutdown settings
  shutdown_command = "sudo shutdown -P now"
  shutdown_timeout = "5m"
}

# Build steps
build {
  name = "birdnet-go-vm"
  sources = ["source.qemu.birdnet-go"]
  
  # Wait for cloud-init to complete
  provisioner "shell" {
    inline = [
      "echo 'Waiting for cloud-init to complete...'",
      "sudo cloud-init status --wait",
      "echo 'Cloud-init completed'"
    ]
  }
  
  # Update system packages
  provisioner "shell" {
    inline = [
      "echo 'Updating system packages...'",
      "sudo apt-get update -q",
      "sudo apt-get upgrade -y -q",
      "sudo apt-get install -y -q curl wget gnupg lsb-release ca-certificates",
      "echo 'System packages updated'"
    ]
  }
  
  # Install Docker
  provisioner "shell" {
    inline = [
      "echo 'Installing Docker...'",
      "curl -fsSL https://download.docker.com/linux/ubuntu/gpg | sudo gpg --dearmor -o /usr/share/keyrings/docker-archive-keyring.gpg",
      "echo \"deb [arch=${var.arch == "amd64" ? "amd64" : "arm64"} signed-by=/usr/share/keyrings/docker-archive-keyring.gpg] https://download.docker.com/linux/ubuntu $(lsb_release -cs) stable\" | sudo tee /etc/apt/sources.list.d/docker.list > /dev/null",
      "sudo apt-get update -q",
      "sudo apt-get install -y -q docker-ce docker-ce-cli containerd.io docker-compose-plugin",
      "sudo usermod -aG docker birdnet",
      "sudo systemctl enable docker",
      "echo 'Docker installed'"
    ]
  }
  
  # Install additional packages
  provisioner "shell" {
    inline = [
      "echo 'Installing additional packages...'",
      "sudo apt-get install -y -q alsa-utils bc jq apache2-utils netcat-openbsd iproute2 lsof avahi-daemon libnss-mdns htop iotop nano vim git qemu-guest-agent",
      "sudo systemctl enable qemu-guest-agent",
      "sudo systemctl enable avahi-daemon",
      "echo 'Additional packages installed'"
    ]
  }
  
  # Copy configuration files
  provisioner "file" {
    source      = "files/"
    destination = "/tmp/"
  }
  
  # Setup BirdNET-Go
  provisioner "shell" {
    script = "scripts/setup-birdnet-go.sh"
    environment_vars = [
      "VERSION=${var.version}",
      "ARCH=${var.arch}"
    ]
  }
  
  # Configure system services
  provisioner "shell" {
    script = "scripts/configure-services.sh"
  }
  
  # Optimize and cleanup
  provisioner "shell" {
    script = "scripts/cleanup.sh"
  }
  
  # Generate final configuration
  provisioner "shell" {
    inline = [
      "echo 'Generating system information...'",
      "sudo tee /etc/birdnet-go-vm-info > /dev/null << EOF",
      "BirdNET-Go VM Image",
      "Version: ${var.version}",
      "Architecture: ${var.arch}",
      "Build Date: $(date -u +%Y-%m-%dT%H:%M:%SZ)",
      "Base OS: Ubuntu 24.10 (Oracular Oriole)",
      "Docker Version: $(docker --version)",
      "EOF",
      "echo 'System information generated'"
    ]
  }
  
  # Final system sync and prepare for shutdown
  provisioner "shell" {
    inline = [
      "echo 'Final system preparation...'",
      "sudo sync",
      "sudo fstrim -av || true",
      "echo 'System ready for shutdown'"
    ]
  }
  
  # Post-processing: compress and generate checksums
  post-processor "shell-local" {
    inline = [
      "echo 'Compressing image...'",
      "cd ${var.output_dir}-${var.arch}",
      "zstd -19 --rm birdnet-go-vm-${var.arch}-${var.version}.qcow2",
      "echo 'Generating checksums...'",
      "sha256sum birdnet-go-vm-${var.arch}-${var.version}.qcow2.zst > birdnet-go-vm-${var.arch}-${var.version}.qcow2.zst.sha256",
      "echo 'Image processing completed'"
    ]
  }
} 