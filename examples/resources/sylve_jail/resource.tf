resource "sylve_jail" "example" {
  ctid = 200 # 1-9999, unique, chosen by you -- never auto-assigned
  name = "example-jail"
  type = "freebsd" # or "linux"

  # pool/base/switch_name are all required by Sylve itself for a real
  # create, but write-only in this resource (not read back), so declare
  # them for a fresh create and OMIT them entirely if you're importing
  # an existing jail you don't intend to recreate:
  pool        = "tank"
  base        = sylve_download.freebsd_base.uuid # a download with utype = "base-rootfs", already extracted
  switch_name = "lan"                            # or "none" for no networking, or "inherit"

  cores  = 2
  memory = 1073741824 # 1GiB, in BYTES -- not MiB
}
