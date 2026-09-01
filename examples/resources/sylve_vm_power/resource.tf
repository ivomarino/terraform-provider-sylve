# Real, drift-detected state -- if someone starts/stops the VM outside
# Terraform, the next plan shows it as a diff, same as any other
# attribute. Separate from sylve_vm itself so creating a VM doesn't
# implicitly start it.
resource "sylve_vm_power" "example" {
  rid   = sylve_vm.example.rid
  state = "running" # or "stopped"
}
