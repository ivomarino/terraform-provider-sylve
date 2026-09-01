# Requires: qemu_guest_agent enabled on the VM, the guest-agent package
# actually installed and running inside the guest (nothing installs it
# automatically -- see sylve_vm's cloud_init attributes for one way to
# get it there), and the VM running. Errors with a timeout if the agent
# isn't there to answer.
data "sylve_vm_guest_agent" "example" {
  rid = sylve_vm.example.rid
}

output "guest_os" {
  value = data.sylve_vm_guest_agent.example.os_pretty_name
}

output "guest_interfaces" {
  value = data.sylve_vm_guest_agent.example.interfaces
}
