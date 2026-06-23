# External Media (USB, SD Card, File Shares)

BirdNET-Go can read from and write to removable or network-attached storage
mounted on the host. The `/external` directory inside the container maps to
`/mnt/birdnet-go/external` on the host. Planned uses include importing
recordings from BirdNET-Pi and other bird detectors, and writing backups.

## How it works

The container bind-mounts `/mnt/birdnet-go/external` with `rslave` propagation.
Any media the host mounts under that directory after the container is already
running will appear inside the container at the corresponding path without
requiring a container restart. For example, a USB drive the host mounts at
`/mnt/birdnet-go/external/usb1` becomes visible inside the container at
`/external/usb1` automatically.

## Host setup

### install.sh (systemd) installations

The installer-generated systemd service handles this automatically on every
container start:

1. Creates `/mnt/birdnet-go/external` (mode 0755) if it does not exist.
2. Bind-mounts it to itself if it is not already a mount point.
3. Marks it as a shared mount (`--make-rshared`) so sub-mounts propagate into
   the container.

No manual steps are needed for install.sh users. Re-running the installer after
upgrading regenerates the service unit and picks up these steps.

### Docker Compose installations

Docker Compose users need a one-time host setup before starting the container:

```bash
sudo mkdir -p /mnt/birdnet-go/external
sudo mount --bind /mnt/birdnet-go/external /mnt/birdnet-go/external
sudo mount --make-rshared /mnt/birdnet-go/external
```

To make this persistent across reboots, add the following to `/etc/fstab`:

```
/mnt/birdnet-go/external /mnt/birdnet-go/external none bind,x-systemd.after=local-fs.target 0 0
```

Then add a systemd `ExecStartPre` step or use a one-shot service to run
`mount --make-rshared /mnt/birdnet-go/external` before the container starts.

Note: if the host root filesystem is already `rshared` (common on modern systemd
distributions), the bind step may not be strictly required, but doing it
idempotently does no harm.

## Mounting media on the host

Once the host setup is complete, mount any USB drive, SD card, or file share
under `/mnt/birdnet-go/external`:

```bash
# USB drive example (replace /dev/sdb1 and label as appropriate)
sudo mkdir -p /mnt/birdnet-go/external/usb1
sudo mount /dev/sdb1 /mnt/birdnet-go/external/usb1

# NFS share example
sudo mkdir -p /mnt/birdnet-go/external/nfs-backup
sudo mount -t nfs 192.168.1.10:/exports/birdnet /mnt/birdnet-go/external/nfs-backup

# SMB/CIFS share example
sudo mkdir -p /mnt/birdnet-go/external/smb-share
sudo mount -t cifs //192.168.1.10/share /mnt/birdnet-go/external/smb-share \
    -o username=user,password=pass,uid=1000,gid=1000
```

The mounted media will appear inside the container at `/external/usb1`,
`/external/nfs-backup`, etc.

## Notes

- Hot-plug propagation requires the host shared-mount setup described above.
  It has been verified in configuration but needs testing on a real Docker host
  with physical media.
- The `/external` path is read-write inside the container.
- Writes from the container go to the mounted media on the host. Ensure the
  media filesystem and mount permissions allow writes by the container user
  (UID/GID set via `BIRDNET_UID` / `BIRDNET_GID`).
