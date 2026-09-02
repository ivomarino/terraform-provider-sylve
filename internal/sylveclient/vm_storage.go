package sylveclient

import (
	"context"
	"fmt"
)

// VMStorage is one disk/CD-ROM attached to a VM, as nested under GET
// /vm/:id's own storages[] array -- there is no per-storage GET
// endpoint, only this nested view (reached via GetVMStorage below) and
// the mutation endpoints (attach/update/detach).
type VMStorage struct {
	ID               int    `json:"id"`
	Type             string `json:"type"` // "raw", "zvol", "image", or "filesystem"
	Name             string `json:"name"`
	DownloadUUID     string `json:"uuid"` // populated only for type "image"
	Pool             string `json:"pool"`
	Enable           bool   `json:"enable"`
	Size             int64  `json:"size"`
	Emulation        string `json:"emulation"`
	FilesystemTarget string `json:"filesystemTarget"`
	ReadOnly         bool   `json:"readOnly"`
	BootOrder        int    `json:"bootOrder"`
}

// AttachStorage attaches a disk/CD-ROM to a (shut-off -- see below) VM.
// POST /api/vm/storage/attach. Two attachType values, each needing
// different fields:
//
//   - "new": storageType raw/zvol creates a fresh empty disk (pool,
//     size required); storageType filesystem needs an existing dataset
//     GUID + filesystemTarget.
//   - "import": storageType "zvol" adopts an EXISTING zvol dataset
//     (dataset = its GUID, pool = the pool it's being imported into --
//     typically already flashed via Client.FlashVolume, see that
//     method's own doc comment) by renaming/cloning it into the VM's
//     own dataset namespace; storageType "image" attaches a download
//     directly by reference (downloadUUID), with a caller-chosen
//     emulation -- unlike a VM's create-time `iso` field, which always
//     hardcodes "ahci-cd", this can be "virtio-blk" etc., making it
//     usable as a real boot disk, not just CD-ROM media.
//
// The target VM MUST already be stopped -- Sylve rejects this outright
// with domain_state_not_shutoff otherwise; this client makes no attempt
// to stop/start the VM itself.
type AttachStorageParams struct {
	RID              int
	Name             string
	AttachType       string // "new" or "import"
	StorageType      string // "raw", "zvol", "image", or "filesystem"
	Emulation        string
	Pool             string
	Size             int64
	Dataset          string // source zvol GUID, for attachType "import" + storageType "zvol"
	DownloadUUID     string // for attachType "import" + storageType "image"
	RawPath          string // for attachType "import" + storageType "raw"
	FilesystemTarget string
	ReadOnly         bool
	BootOrder        int
	HasBootOrder     bool
}

func (c *Client) AttachStorage(ctx context.Context, p AttachStorageParams) error {
	// v0.3.0: POST /api/vm/{rid}/storage -- rid moved to the URL path and
	// out of the request body entirely (StorageAttachRequest.RID is
	// `json:"-"` now); every other field name unchanged.
	body := map[string]any{
		"name":             p.Name,
		"attachType":       p.AttachType,
		"storageType":      p.StorageType,
		"emulation":        p.Emulation,
		"downloadUUID":     p.DownloadUUID,
		"rawPath":          p.RawPath,
		"dataset":          p.Dataset,
		"filesystemTarget": p.FilesystemTarget,
		"readOnly":         p.ReadOnly,
	}
	if p.Pool != "" {
		body["pool"] = p.Pool
	}
	if p.Size > 0 {
		body["size"] = p.Size
	}
	if p.HasBootOrder {
		body["bootOrder"] = p.BootOrder
	}
	if err := c.do(ctx, "POST", fmt.Sprintf("/api/vm/%d/storage", p.RID), body, nil); err != nil {
		return fmt.Errorf("attaching storage %q to VM rid %d: %w", p.Name, p.RID, err)
	}
	return nil
}

// UpdateStorageParams mirrors StorageUpdateRequest. v0.3.0 needs BOTH
// the VM's RID and the storage's own ID (both moved into the URL path,
// `PATCH /api/vm/{rid}/storage/{storageId}`) -- v0.2.x only needed the
// storage ID (`PUT /api/vm/storage/update`, id-only in the body); RID is
// a new required field on this struct as of this compat pass.
type UpdateStorageParams struct {
	RID              int
	ID               int
	Name             string
	Emulation        string
	Size             int64
	HasSize          bool
	BootOrder        int
	HasBootOrder     bool
	Enable           bool
	FilesystemTarget string
	ReadOnly         bool
}

// UpdateStorage changes an attached disk's mutable properties. v0.3.0:
// PATCH /api/vm/{rid}/storage/{storageId} (was PUT /api/vm/storage/update,
// id-in-body only, in v0.2.x).
func (c *Client) UpdateStorage(ctx context.Context, p UpdateStorageParams) error {
	body := map[string]any{
		"name":             p.Name,
		"emulation":        p.Emulation,
		"enable":           p.Enable,
		"filesystemTarget": p.FilesystemTarget,
		"readOnly":         p.ReadOnly,
	}
	if p.HasSize {
		body["size"] = p.Size
	}
	if p.HasBootOrder {
		body["bootOrder"] = p.BootOrder
	}
	return c.do(ctx, "PATCH", fmt.Sprintf("/api/vm/%d/storage/%d", p.RID, p.ID), body, nil)
}

// DetachStorage removes a disk/CD-ROM from a VM. v0.3.0: DELETE
// /api/vm/{rid}/storage/{storageId}, empty body -- both rid and
// storageId are now path params (was POST /api/vm/storage/detach with
// both in the body). This IS the real delete (matches the source's own
// StorageDetach -> permanent removal, not a reversible unplug); the
// target VM must be shut off, same constraint as AttachStorage.
func (c *Client) DetachStorage(ctx context.Context, rid, storageID int) error {
	return c.do(ctx, "DELETE", fmt.Sprintf("/api/vm/%d/storage/%d", rid, storageID), nil, nil)
}

// GetVMStorage finds one storage entry nested under a VM by its own ID.
// There is no per-storage GET endpoint -- only GET /vm/:id's own
// storages[] array. Returns an error satisfying IsNotFound if the VM or
// the specific storage entry no longer exists.
func (c *Client) GetVMStorage(ctx context.Context, rid, storageID int) (*VMStorage, error) {
	vm, err := c.GetVM(ctx, rid)
	if err != nil {
		return nil, err
	}
	for _, s := range vm.Storages {
		if s.ID == storageID {
			return &s, nil
		}
	}
	return nil, &apiError{StatusCode: 404, Body: fmt.Sprintf("storage id %d not found on VM rid %d", storageID, rid)}
}

// FindVMStorageByName finds a storage entry by name -- used right after
// AttachStorage, whose response doesn't carry the new entry's ID.
func (c *Client) FindVMStorageByName(ctx context.Context, rid int, name string) (*VMStorage, error) {
	vm, err := c.GetVM(ctx, rid)
	if err != nil {
		return nil, err
	}
	for _, s := range vm.Storages {
		if s.Name == name {
			return &s, nil
		}
	}
	return nil, &apiError{StatusCode: 404, Body: fmt.Sprintf("storage %q not found on VM rid %d after attach", name, rid)}
}
