# Changelog

All notable changes to this provider are documented here. Format loosely
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); this
project has no tagged releases yet, so everything to date lives under
`Unreleased`.

## Unreleased

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
