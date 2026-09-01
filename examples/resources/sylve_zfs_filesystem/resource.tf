resource "sylve_zfs_filesystem" "example" {
  name   = "my-fs" # leaf name -> full path "tank/my-fs"
  parent = "tank"

  properties = {
    compression = "lz4"
    quota       = "10G"
  }
}
