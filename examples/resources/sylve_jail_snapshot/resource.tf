# Structurally identical to sylve_vm_snapshot -- see that resource's
# example for the shared reasoning (crash-consistent, immutable
# name/description).
resource "sylve_jail_snapshot" "before_upgrade" {
  ctid        = sylve_jail.example.ctid
  name        = "before-upgrade"
  description = "before applying package upgrades"
}
