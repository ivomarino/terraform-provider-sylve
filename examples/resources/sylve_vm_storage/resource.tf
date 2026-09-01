# Importing an existing (typically already-flashed, see
# sylve_zfs_volume's flash_from_download_uuid) zvol as a VM's real boot
# disk. The target VM must be stopped for this to succeed.
resource "sylve_vm_storage" "boot_disk" {
  rid          = sylve_vm.example.rid
  name         = "boot-disk"
  attach_type  = "import"
  storage_type = "zvol"
  emulation    = "virtio-blk" # unlike sylve_vm's own `iso`, this can be a real boot device, not just CD-ROM
  pool         = "tank"
  dataset      = sylve_zfs_volume.example.id
}

# A fresh, empty additional disk instead:
resource "sylve_vm_storage" "extra_disk" {
  rid          = sylve_vm.example.rid
  name         = "extra-disk"
  attach_type  = "new"
  storage_type = "zvol"
  emulation    = "virtio-blk"
  pool         = "tank"
  size         = 21474836480 # 20GiB
}
