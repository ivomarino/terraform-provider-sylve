# A one-shot ACTION, not persistent state -- modeled like
# hashicorp/null's null_resource. Applying this the first time performs
# the rollback immediately. Nothing happens again unless `trigger`
# changes, which recreates the resource and fires the rollback again.
# Destroying this resource does NOT undo anything -- there is no
# "rollback the rollback"; take a new sylve_vm_snapshot first if you
# might want to go back.
resource "sylve_vm_snapshot_rollback" "restore" {
  rid         = sylve_vm.example.rid
  snapshot_id = tonumber(sylve_vm_snapshot.before_upgrade.id)
  trigger     = "restore-1" # bump this to any new value to fire the rollback again
}
