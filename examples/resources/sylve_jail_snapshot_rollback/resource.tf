# One-shot ACTION -- see sylve_vm_snapshot_rollback's own example for
# the shared reasoning (trigger + RequiresReplace to re-fire,
# deliberately no-op Delete).
resource "sylve_jail_snapshot_rollback" "restore" {
  ctid        = sylve_jail.example.ctid
  snapshot_id = tonumber(sylve_jail_snapshot.before_upgrade.id)
  trigger     = "restore-1"
}
