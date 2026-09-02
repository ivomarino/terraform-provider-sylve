# Changelog

All notable changes to this provider are documented here. Format loosely
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); this
project has no tagged releases yet, so everything to date lives under
`Unreleased`.

## Unreleased

### Changed

- **Sylve `v0.3.0` compatibility (in progress).** Sylve's own `v0.3.0`
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
  - Further `sylve_vm`/`sylve_jail` sub-resource updates (storage,
    network, snapshots, lifecycle actions, hardware/options endpoints)
    are in progress — most of this provider's route surface changed
    shape in the same direction (an identifier moved from a body field
    or path suffix to a path prefix), tracked as this section grows.
  - `sylve_zfs_filesystem`/`sylve_zfs_volume`/`sylve_network_object`/
    `sylve_download` are believed unaffected (same paths in both
    versions) — being verified live as each is reached, not assumed.

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
