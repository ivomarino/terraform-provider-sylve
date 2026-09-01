# terraform-provider-sylve

A Terraform provider for [Sylve](https://sylve.io/) — a lightweight,
Proxmox-alike management plane for bhyve VMs, jails, and ZFS on FreeBSD.

Not affiliated with the Sylve project or [AlchemillaHQ](https://github.com/AlchemillaHQ/Sylve).
Not published to the Terraform Registry (yet) — see [Installing](#installing) below
for how to use it locally in the meantime.

Every request/response shape here was derived from Sylve's actual
source (`v0.2.3`), not just its swagger spec — the two disagree in
several places, and the swagger spec undercounts real required-ness on
more than one field. Every resource has been exercised against a real,
running Sylve instance: create, read-after-import, in-place update,
delete, and drift detection where applicable. A handful of genuinely
surprising bugs (both in this provider's own early assumptions, and in
Sylve itself) were found this way — see [Known quirks](#known-quirks-worth-knowing-before-you-hit-them)
below.

## Status

Early, functional, not yet published to any registry. API surface
covers VM and jail lifecycle, ZFS-backed storage, downloads (ISOs/cloud
images/jail base images), manual switches, network objects, snapshots +
rollback, and basic power management — see [Resources](#resources)
below for the full list. Standard (VLAN/DHCP-capable) network switches
are deliberately not covered — see [Known limitations](#known-limitations).

## Installing

Since this isn't published yet, use Terraform's
[`dev_overrides`](https://developer.hashicorp.com/terraform/cli/config/config-file#development-overrides-for-provider-developers)
to point at a local build:

```bash
go build -o bin/terraform-provider-sylve .
```

```hcl
# ~/.terraformrc
provider_installation {
  dev_overrides {
    "registry.terraform.io/ivomarino/sylve" = "/absolute/path/to/terraform-provider-sylve/bin"
  }
  direct {}
}
```

With `dev_overrides` active, `terraform init` won't (and can't) actually
download anything for this provider — that's expected.

## Provider configuration

```hcl
terraform {
  required_providers {
    sylve = {
      source = "ivomarino/sylve"
    }
  }
}

provider "sylve" {
  endpoint  = "https://sylve.example.com:8181" # or SYLVE_ENDPOINT
  username  = "admin"                          # or SYLVE_USERNAME -- the users.username value, NOT an email
  password  = var.sylve_password               # or SYLVE_PASSWORD -- never hardcode this
  auth_type = "sylve"                          # or SYLVE_AUTH_TYPE -- "sylve" (default) or "pam"
}
```

All five attributes are optional in HCL and fall back to the matching
`SYLVE_*` environment variable, so a config that reads secrets from the
environment can omit the `provider` block's sensitive fields entirely.

One gotcha worth knowing up front: **log in with the account's
`username`, not its email.** Sylve's own seed admin account is
identified by an email in its `config.json` (`admin@sylve.local`), but
the login API wants the `users.username` column value (`admin`) — the
email gets a flat `invalid_credentials` rejection.

## Resources

| Resource | Covers |
|---|---|
| `sylve_vm` | A bhyve VM: CPU/RAM/VNC/boot options, first disk + first NIC, boot/cloud-init media, cloud-init payloads |
| `sylve_vm_storage` | An additional disk/CD-ROM beyond the VM's create-time first one — the real mechanism for a cloud-image-backed boot disk |
| `sylve_vm_network` | An additional NIC beyond the VM's create-time first one |
| `sylve_vm_power` | Whether a VM is running or stopped — real, drift-detected state |
| `sylve_vm_snapshot` / `sylve_vm_snapshot_rollback` | ZFS-backed VM snapshots, and a one-shot action to roll back to one |
| `sylve_jail` | A FreeBSD/Linux jail, structurally parallel to `sylve_vm` |
| `sylve_jail_snapshot` / `sylve_jail_snapshot_rollback` | The jail equivalents of the VM snapshot resources |
| `sylve_zfs_filesystem` | A ZFS filesystem dataset |
| `sylve_zfs_volume` | A ZFS volume (zvol), with an optional one-shot flash-from-download |
| `sylve_download` | An ISO, cloud image, or jail base-rootfs archive — HTTP, torrent, or local-path sourced |
| `sylve_manual_switch` | A named label on an already-existing bridge interface |
| `sylve_network_object` | A named set of values (MAC/IP/host/port/country/list) |

## Data sources

| Data source | Covers |
|---|---|
| `sylve_vm_guest_agent` | Live guest OS info and network interfaces, via the QEMU guest agent channel |

## Example: a Debian VM from a real cloud image

This is the flagship end-to-end workflow this provider was built
around, and the one every piece above was ultimately verified against.
Turning a downloaded cloud image into an actual bootable VM disk is a
three-step dance in Sylve's own API — not obvious from `POST /vm`
alone — which this example walks through in full:

```hcl
# 1. Pull the image. A .raw-format cloud image (Debian publishes one
#    directly) sidesteps needing automatic_raw_conversion.
resource "sylve_download" "debian" {
  url   = "https://cloud.debian.org/images/cloud/trixie/latest/debian-13-genericcloud-amd64.raw"
  utype = "cloud-init"
}

# 2. Get it onto a ZFS volume. flash_from_download_uuid does the actual
#    write -- Sylve's own FlashVolume endpoint, literally a `dd`.
resource "sylve_zfs_volume" "debian_disk" {
  name   = "my-debian-vm"
  parent = "tank"
  properties = {
    size = "4294967296" # 4GiB, comfortably above the image's own size
  }
  flash_from_download_uuid = sylve_download.debian.uuid
}

# 3. Create the VM with no disk of its own (storage_type = "none") --
#    the flashed volume gets attached separately, below.
resource "sylve_vm" "debian" {
  rid         = 100
  name        = "my-debian-vm"
  ram         = 2147483648 # 2GiB, in BYTES -- not MiB
  cpu_sockets = 1
  cpu_cores   = 2
  cpu_threads = 1
  vnc_port    = 5900

  storage_type = "none"

  switch_name           = "lan" # an existing sylve_manual_switch or standard switch
  switch_emulation_type = "virtio"

  # cloud-init: SSH key injection + a static IP, entirely via cloud-init
  # itself -- Sylve auto-generates the NoCloud seed ISO from these three
  # fields, no separate ISO-building step needed. `iso` below just needs
  # to point at *some* download tagged "cloud-init" to satisfy a
  # validation gate; it isn't the seed data itself.
  cloud_init = true
  iso        = sylve_download.debian.uuid

  cloud_init_data = <<-EOT
    #cloud-config
    hostname: my-debian-vm
    users:
      - name: debian
        sudo: ALL=(ALL) NOPASSWD:ALL
        ssh_authorized_keys:
          - ssh-ed25519 AAAA... you@example.com
    packages:
      - qemu-guest-agent
    runcmd:
      - systemctl enable --now qemu-guest-agent
  EOT

  cloud_init_metadata = <<-EOT
    instance-id: my-debian-vm
    local-hostname: my-debian-vm
  EOT

  # Reference the interface by its concrete name (as cloud-init's own
  # log reports it, e.g. "enp0s3") -- Debian's default cloud-init
  # renderer does NOT support netplan/networkd-style `match:`/`set-name`
  # v2 syntax. Using it silently leaves the NIC unconfigured; see "Known
  # quirks" below.
  cloud_init_network_config = <<-EOT
    version: 2
    ethernets:
      enp0s3:
        dhcp4: false
        addresses:
          - 192.0.2.50/24
        gateway4: 192.0.2.1
        nameservers:
          addresses:
            - 192.0.2.1
  EOT

  qemu_guest_agent = true
}

# 4. Attach the flashed volume as the VM's real boot disk. This is what
#    makes it bootable, not CD-ROM-only media -- storage_type = "zvol" +
#    attach_type = "import" adopts the volume, and emulation is the
#    caller's own choice here (unlike sylve_vm's own `iso`, which always
#    hardcodes CD-ROM emulation).
resource "sylve_vm_storage" "debian_boot_disk" {
  rid          = sylve_vm.debian.rid
  name         = "boot-disk"
  attach_type  = "import"
  storage_type = "zvol"
  emulation    = "virtio-blk"
  pool         = "tank"
  dataset      = sylve_zfs_volume.debian_disk.id
}

# 5. Boot it.
resource "sylve_vm_power" "debian" {
  rid   = sylve_vm.debian.rid
  state = "running"

  depends_on = [sylve_vm_storage.debian_boot_disk]
}

# 6. Confirm it's actually alive, once running.
data "sylve_vm_guest_agent" "debian" {
  rid = sylve_vm.debian.rid

  depends_on = [sylve_vm_power.debian]
}

output "debian_ip" {
  value = data.sylve_vm_guest_agent.debian.interfaces
}
```

## Known quirks worth knowing before you hit them

Sylve's real behavior differs from what its own request struct tags or
swagger spec would suggest in several places. All of these were found
by testing against a live instance, not by reading source alone:

- **`ram` is in bytes, not MiB.**
- **`rid`/`ctid` are never auto-assigned.** Pick one yourself, 1-9999,
  unique — same as a Proxmox VMID.
- **`vnc_wait` defaults to `true` if you never set it** — via this
  provider's own API, not Terraform. Omitting the underlying field
  entirely (which an early version of this provider did) pauses the
  guest's vCPUs forever, waiting for a VNC client that's never coming,
  while the VM still reports a "Running" domain status. This provider
  now always sends an explicit value (default `false`), but if you're
  calling Sylve's API directly, know that omission ≠ the Go zero value
  here.
- **`storage_type` needs the literal string `"none"`** to mean "no
  disk" — an empty string is not equivalent, and looks up a ZFS pool
  named `""` instead.
- **`switch_name` needs a matching `switch_emulation_type`.** Setting
  one without the other is rejected outright.
- **VM/jail delete needs explicit query params** (`deletemacs`,
  `deleterawdisks`, `deletevolumes` for VMs; `deletemacs`,
  `deleterootfs` for jails) — none default to a value.
- **cloud-init's actual mechanism**: Sylve auto-generates a NoCloud seed
  ISO from `cloud_init_data`/`cloud_init_metadata`/`cloud_init_network_config`
  at VM start. `iso` itself only needs to resolve to *some* download
  tagged `utype = "cloud-init"` — it's a validation gate, not the seed
  data. And Debian's own cloud-init renderer does not support
  networkd/netplan-style `match:`/`set-name` syntax in
  `cloud_init_network_config` — reference the interface by its concrete
  name instead.
- **A ZFS filesystem's `parent` is silently discarded** by Sylve's own
  create API — the real value has to also be present inside
  `properties["parent"]`. This provider hides that behind one `parent`
  argument; direct API callers need to know both.

## Known limitations

- **Standard (VLAN/DHCP-capable) network switches are not implemented.**
  Creating one requires binding a real physical NIC as a switch port —
  on a single-NIC host, that risks severing the very connection managing
  it. Treat this the same way you'd treat any change to your only
  management interface: never apply blind.
- No DHCP resources (config/range/lease), Samba, Users/Groups, Cluster,
  or System (PCI passthrough, file explorer, disk wipe/partition)
  coverage yet.
- No secondary-NIC MAC pinning validation, no ZFS pool management
  (creating/destroying pools is destructive enough to warrant care this
  provider doesn't yet take).

## Contributing

Issues and PRs welcome. If you're adding a resource, please verify it
against a real Sylve instance before opening a PR — this codebase has
already found multiple cases where the source's own struct tags and the
swagger spec disagreed with actual runtime behavior.

## License

[Mozilla Public License 2.0](LICENSE).
