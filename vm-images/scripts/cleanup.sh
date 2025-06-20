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