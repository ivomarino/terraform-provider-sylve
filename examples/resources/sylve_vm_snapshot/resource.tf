# Crash-consistent regardless of whether the VM is running or stopped.
# name/description are immutable -- there's no rename endpoint, so
# changing either recreates the snapshot (deletes the old one, takes a
# fresh one) rather than editing history in place.
resource "sylve_vm_snapshot" "before_upgrade" {
  rid         = sylve_vm.example.rid
  name        = "before-upgrade"
  description = "before applying package upgrades"
}
