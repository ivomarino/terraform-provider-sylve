# A named label on an already-existing bridge interface -- Sylve does
# NOT create the bridge itself (unlike a "standard" switch, which this
# provider deliberately doesn't implement, see the README). Create the
# bridge out of band first, e.g.:
#   ssh sylve-host 'ifconfig bridge10 create'
resource "sylve_manual_switch" "lan" {
  name   = "lan"
  bridge = "bridge10"
}
