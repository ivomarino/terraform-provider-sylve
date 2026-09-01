resource "sylve_network_object" "static_mac" {
  name   = "my-vm-mac"
  type   = "Mac" # or "Host", "Network", "Port", "Country", "List"
  values = ["AA:BB:CC:DD:EE:01"]
}

resource "sylve_network_object" "office_subnet" {
  name   = "office-subnet"
  type   = "Network"
  values = ["192.0.2.0/24"]
}
