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

1. Creates `/mnt/birdnet-go/external` if it does not exist.
2. Bind-mounts it to itself if it is not already a mount point.
3. Marks it as a shared mount (`--make-rshared`) so sub-mounts propagate into
   the container.

No manual steps are needed for install.sh users. Re-running the installer after
upgrading regenerates the service unit and picks up these steps.

### Docker Compose installations

The external media volume is commented out in `docker-compose.yml` by default.
`docker compose up` works without any host setup. To enable it, perform the
one-time host setup below, then uncomment the bind block in `docker-compose.yml`.

#### One-time manual setup

```bash
sudo mkdir -p /mnt/birdnet-go/external
sudo mount --bind /mnt/birdnet-go/external /mnt/birdnet-go/external
sudo mount --make-rshared /mnt/birdnet-go/external
```

An fstab bind entry alone does NOT establish `rshared` propagation and is
therefore not sufficient for hot-plug to work. Both the self-bind and the
`make-rshared` step are required.

#### Making the setup persistent across reboots

Create a one-shot systemd service that runs both steps in the correct order and
is guaranteed to complete before the container service starts:

```ini
# /etc/systemd/system/birdnet-external-media.service
[Unit]
Description=Prepare BirdNET-Go external media mount point
DefaultDependencies=no
After=local-fs.target
Before=docker.service

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/bin/mkdir -p /mnt/birdnet-go/external
ExecStart=/bin/sh -c 'mountpoint -q /mnt/birdnet-go/external || mount --bind /mnt/birdnet-go/external /mnt/birdnet-go/external'
ExecStart=/bin/sh -c 'mount --make-rshared /mnt/birdnet-go/external'

[Install]
WantedBy=multi-user.target
RequiredBy=docker.service
```

Enable it with:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now birdnet-external-media.service
```

Note: if the host root filesystem is already `rshared` (common on modern systemd
distributions), the bind step may not be strictly required, but running it
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
  The wiring is in place, but hot-plug propagation has NOT been tested end to end
  with physical media and must be validated before relying on it.
- The `/external` path is read-write inside the container.
- Writes from the container go to the mounted media on the host. Ensure the
  media filesystem and mount permissions allow writes by the container user
  (UID/GID set via `BIRDNET_UID` / `BIRDNET_GID`).
