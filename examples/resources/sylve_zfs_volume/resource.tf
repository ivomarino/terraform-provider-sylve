resource "sylve_zfs_volume" "example" {
  name   = "my-vol" # leaf name -> full path "tank/my-vol"
  parent = "tank"

  properties = {
    size = "10737418240" # 10GiB -- REQUIRED inside properties; no separate structured size field exists
  }

  # Optional: write the contents of a sylve_download (e.g. a cloud
  # image) directly onto this volume once it's created -- the actual
  # mechanism for a cloud-image-backed VM disk, see sylve_vm_storage.
  # flash_from_download_uuid = sylve_download.example.uuid
}
