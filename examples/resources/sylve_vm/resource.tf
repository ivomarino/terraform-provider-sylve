resource "sylve_vm" "example" {
  rid         = 100 # 1-9999, unique, chosen by you -- never auto-assigned
  name        = "example-vm"
  description = "created by terraform"

  ram         = 1073741824 # 1GiB, in BYTES -- not MiB
  cpu_sockets = 1
  cpu_cores   = 2
  cpu_threads = 1

  vnc_port = 5900
  # vnc_wait defaults to false -- leave unset for normal headless use

  # First disk, created fresh (empty) at VM creation time. For a disk
  # seeded from a downloaded image, use storage_type = "none" here and
  # attach one separately via sylve_vm_storage instead.
  storage_type           = "zvol"
  storage_pool           = "tank"
  storage_size           = 10737418240 # 10GiB
  storage_emulation_type = "virtio-blk"

  # First NIC. switch_emulation_type is required whenever switch_name is set.
  switch_name           = "lan" # an existing sylve_manual_switch or standard switch
  switch_emulation_type = "virtio"

  qemu_guest_agent = true
  start_at_boot    = false
}
