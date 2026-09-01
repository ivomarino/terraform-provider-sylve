# An additional NIC beyond the VM's create-time first one. No shutoff
# requirement -- unlike sylve_vm_storage, this works on a running VM.
resource "sylve_vm_network" "extra_nic" {
  rid         = sylve_vm.example.rid
  switch_name = "lan"
  emulation   = "virtio"
  # mac_id = sylve_network_object.example.id # omit to let Sylve auto-generate one
}
