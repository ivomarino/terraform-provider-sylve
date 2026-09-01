# An HTTP-sourced cloud image.
resource "sylve_download" "debian" {
  url   = "https://cloud.debian.org/images/cloud/trixie/latest/debian-13-genericcloud-amd64.raw"
  utype = "cloud-init"
}

# A jail base rootfs archive, auto-extracted after download.
resource "sylve_download" "freebsd_base" {
  url                  = "https://download.freebsd.org/releases/amd64/15.1-RELEASE/base.txz"
  utype                = "base-rootfs"
  automatic_extraction = true
}

# An already-present local file, registered with no network fetch at
# all -- Sylve auto-detects this from the shape of `url` (an absolute
# filesystem path, rather than a URL or magnet URI).
resource "sylve_download" "local_iso" {
  url = "/path/on/the/sylve/host/custom.iso"
}
