# Changelog

All notable changes to this provider are documented here. Format loosely
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); this
project has no tagged releases yet, so everything to date lives under
`Unreleased`.

## Unreleased

### Documentation

- **Debian 13 (trixie) cloud-init on this provider: three real bugs,
  found standing up a fresh genericcloud VM end-to-end.** No provider
  code changed for these — they're operational findings, added to
  README's "Known quirks" and the flagship cloud-init example, since the
  example as previously written would reproduce an indefinite boot hang
  on Debian 13 specifically:
  - `systemd-networkd-wait-online.service` can hang the boot forever,
    regardless of static/DHCP content in `cloud_init_network_config` —
    a documented, known Debian-13 + cloud-init bug (matching an open
    systemd upstream issue), not anything specific to Sylve or this
    provider. Fix: bypass `cloud_init_network_config`'s own rendering
    entirely via `network: {config: disabled}` + a hand-written
    `systemd-networkd` unit, with `systemctl mask
    systemd-networkd-wait-online.service` alongside it.
  - `qemu-guest-agent` is not pre-installed on Debian's genericcloud
    image (a wrong assumption in the original example) — needs
    installing as a *late* `runcmd` step, not `packages:`, to avoid
    racing the network-config fix above.
  - A real debugging trap: a VM's raw bhyve tap name (e.g. `tap6`) never
    appears in a host bridge's live member list even when the VM is
    perfectly healthy — libvirt renames it to `vnetN` on successful
    attach. Match on the tap-side MAC (same last 5 bytes as the guest's
    own MAC) to confirm which `vnetN` is which, not the tap name.
- Corrected a stale version reference: the intro claimed request/response
  shapes were derived from Sylve `v0.2.3` source, which was true when
  first written but not since the `v0.3.0` compatibility pass below —
  now says `v0.3.0`.

### Changed

- **Sylve `v0.3.0` compatibility.** Sylve's own `v0.3.0`
  release normalized endpoint paths, HTTP methods, and validation across
  most of its REST API. This provider was built and verified against
  `v0.2.3`'s exact behavior, so a real source-level diff between the two
  tags was done before touching any client code, rather than trusting
  the changelog prose alone.
  - `sylve_manual_switch`: create/delete moved from
    `/api/network/manual-switch` to `/api/network/switch/manual`. List
    path and response shape are unchanged.
  - `sylve_jail`: name/description updates moved from `PUT
    /api/jail/{name,description}` (body-carried DB primary key) to
    `PATCH /api/jail/{ctid}/name` / `.../description` (CTID in the URL).
    **This also fixes a real upstream bug this provider previously
    worked around**: earlier Sylve versions required a jail's internal
    database primary key for these two calls specifically (inconsistent
    with every other jail/VM update endpoint, which all use the
    caller-facing CTID/RID) — Sylve's own `v0.3.0` fixed that
    inconsistency, so the separate DB-id tracking this provider carried
    (`db_id` attribute, `Jail.DBID` field) is being removed as this
    compatibility pass lands, not just left unused.
  - `sylve_jail`: CPU/memory updates moved from `PUT /api/jail/cpu`
    (body-carried CTID) to `PUT /api/jail/{ctid}/hardware/cpu` (CTID in
    the URL); memory equivalent at `.../hardware/ram`. Field names inside
    the request body (`cores`, `memory`) are unchanged.
  - `sylve_vm`/`sylve_jail` sub-resources (storage/network attach,
    update, detach; snapshots create/list/delete/rollback; lifecycle
    actions; every hardware/options endpoint) all moved the same
    direction — an identifier that used to travel in the body or a
    path suffix is now a path prefix. Storage/network *update* and
    *detach* now require both the parent VM/jail's id and the
    sub-resource's own id in the path, where v0.2.x only needed the
    sub-resource id — a new required parameter on
    `UpdateStorage`/`UpdateNetwork` as of this pass.
  - `sylve_zfs_filesystem`/`sylve_zfs_volume`: edit and (for volumes)
    flash moved their target GUID from the request body into the URL
    path. **This one was wrongly predicted "unaffected" by the initial
    route-path diff** — caught by actually checking the resource
    against source rather than trusting that prediction, the same
    discipline this compatibility pass used throughout.
  - `sylve_network_object`: confirmed genuinely unaffected, both by
    source diff and a live create/destroy cycle.
  - `sylve_download`: routes unaffected, but a real behavioral break
    was missed in the initial pass and found later, live, provisioning
    a new download — the default `utype` value ("uncategorized" when
    left unset) was still sending v0.2.3's spelling, "uncategoried" (a
    genuine upstream typo, fixed server-side in v0.3.0's own source but
    never updated in this provider's hardcoded client-side default).
    v0.3.0's stricter server-side validation rejects the old spelling
    outright (`download_request_unprocessable`), so any `sylve_download`
    resource relying on the default (not setting `utype` explicitly)
    would fail to create against a v0.3.0 instance. Fixed in the
    default and the schema doc comment both.
- **Live-verified**, not just compiled: a real `sylve_manual_switch`
  create → clean re-plan → destroy cycle, and a real `sylve_vm`
  create → update (name + CPU count, exercising the renamed-endpoint
  and path-prefix fixes together) → clean re-plan → destroy cycle,
  both against a genuine `v0.3.0` instance. `sylve_jail` create was
  exercised too (confirmed reaching real server-side validation, not a
  routing 404) but not carried through a full cycle — blocked on an
  unrelated base-rootfs prerequisite gap on the test host, not on
  anything this compatibility pass changed.

### Added

- **`sylve_vm`: new `vnc_bind` attribute**, updated in place (no
  recreate). Controls which IP the VNC listener binds to on the
  hypervisor -- defaults to Sylve's own `"127.0.0.1"` if left unset.
  Added because rebinding VNC to a LAN-facing address for testing is a
  real, legitimate thing to want on an already-populated,
  `prevent_destroy`'d VM, and the previous `RequiresReplace` on every
  VNC field made that a destroy/recreate round-trip.

### Fixed

- **`sylve_vm`: `vnc_password` had no way to actually disable VNC
  authentication.** Confirmed directly against Sylve's own source
  (`updateVNC`): any non-empty string, including the literal word
  `"disabled"`, becomes the real VNC password -- there is no special
  sentinel value. `vnc_password = "disabled"` was previously
  `RequiresReplace`'d like the rest of the VNC fields; it's now updated
  in place (alongside `vnc_bind` above), and an **empty string** is the
  only way to get genuine unauthenticated VNC. If you were relying on
  `"disabled"` meaning "no auth", it never did -- that's been a real
  (if silly) password the whole time.
- **`sylve_vm`: a VNC update landing in the same `apply` as a
  cloud-init change could 409.** Both `ModifyVNC` and
  `ModifyCloudInitData` require the VM be `Shutoff` first; each used to
  stop/modify/start independently, so if the VNC block's own restart
  already brought the VM back to `Running` by the time the cloud-init
  block ran, that second call failed with
  `domain_state_not_shutoff` even though nothing about the cloud-init
  change itself was wrong. Fixed by computing one shared "does this
  apply need the VM stopped" flag, stopping once, applying whichever
  group(s) actually changed, and starting back up once.

### OpenTofu compatibility

- **Confirmed working, with one real caveat worth knowing.** Ran real
  `tofu plan`/`tofu apply` against live state with this provider loaded
  via `dev_overrides` -- OpenTofu (tested: 1.12.6) does NOT skip
  dependency-lock-file validation for a dev-override'd provider the way
  classic Terraform does, so `dev_overrides` alone isn't enough for a
  provider that's never published anywhere (this one, for now): both
  `tofu init` and `tofu plan` fail with `Inconsistent dependency lock
  file` / `provider registry ... does not have a provider named ...`.
  This is a confirmed, currently open upstream OpenTofu bug
  ([opentofu/opentofu#1715](https://github.com/opentofu/opentofu/issues/1715)),
  not anything wrong in this provider. **Fix**: add a real
  `filesystem_mirror` for this provider (a legitimate installation
  method with its own lock-file entry, unlike `dev_overrides`) alongside
  `dev_overrides` in your CLI config -- see the updated
  [Installing](README.md#installing) section for the exact layout.
  Once published to a real registry this whole caveat goes away.

## Earlier work (undated, predates this file)

- Initial resource set built and live-verified against a real Sylve
  `v0.2.3` instance: `sylve_vm`, `sylve_manual_switch`,
  `sylve_zfs_filesystem`, `sylve_zfs_volume`, `sylve_download`,
  `sylve_jail`, `sylve_network_object`, `sylve_vm_storage`,
  `sylve_vm_snapshot`(+rollback), `sylve_vm_network`, `sylve_vm_power`,
  `sylve_jail_snapshot`(+rollback), and the `sylve_vm_guest_agent` data
  source — the first data source in this provider.
- Several real upstream bugs found and worked around along the way,
  documented in each resource's own schema description and in
  `README.md`'s "Known quirks" section: RAM/memory fields are bytes, not
  MiB; `rid`/`ctid` are caller-chosen and required (no server-side
  auto-assignment despite looking optional in the API's own request
  struct); a VM created without an explicit `vnc_wait: false` silently
  freezes forever waiting for a VNC client that will never connect in an
  automated context; a ZFS filesystem's `parent` field is required by
  validation but silently discarded server-side unless also duplicated
  inside `properties`.
- CI added (`gofmt`, `go vet`, `go build`, `go test`, `terraform fmt` on
  the example configs), full resource/data-source documentation
  generated via `tfplugindocs`, published as a GitHub Pages site.
