package sylveclient

import (
	"context"
	"fmt"
)

// VMSnapshot is a point-in-time ZFS snapshot of a VM's root dataset(s).
// Unlike storage attach/detach, neither creating nor rolling back a
// snapshot requires the VM to be stopped first (no shutoff check found
// in the source) -- ZFS snapshots are crash-consistent regardless of
// whether the VM is running.
type VMSnapshot struct {
	ID               int      `json:"id"`
	VMID             int      `json:"vmId"`
	RID              int      `json:"rid"`
	ParentSnapshotID *int     `json:"parentSnapshotId"`
	Name             string   `json:"name"`
	Description      string   `json:"description"`
	SnapshotName     string   `json:"snapshotName"` // internal ZFS snapshot name
	RootDatasets     []string `json:"rootDatasets"`
}

type vmSnapshotEnvelope struct {
	Data VMSnapshot `json:"data"`
}

type vmSnapshotListEnvelope struct {
	Data []VMSnapshot `json:"data"`
}

// CreateVMSnapshot takes a snapshot of a VM by RID. Unlike most create
// endpoints in this provider, this one returns the created object
// directly -- no follow-up list/lookup needed.
func (c *Client) CreateVMSnapshot(ctx context.Context, rid int, name, description string) (*VMSnapshot, error) {
	var out vmSnapshotEnvelope
	err := c.do(ctx, "POST", fmt.Sprintf("/api/vm/snapshots/%d", rid),
		map[string]string{"name": name, "description": description}, &out)
	if err != nil {
		return nil, fmt.Errorf("creating snapshot %q of VM rid %d: %w", name, rid, err)
	}
	return &out.Data, nil
}

// ListVMSnapshots returns every snapshot for a VM by RID.
func (c *Client) ListVMSnapshots(ctx context.Context, rid int) ([]VMSnapshot, error) {
	var out vmSnapshotListEnvelope
	if err := c.do(ctx, "GET", fmt.Sprintf("/api/vm/snapshots/%d", rid), nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// GetVMSnapshot finds a snapshot by its own ID. There is no per-snapshot
// GET endpoint, only the per-VM list, so this lists and filters. Returns
// an error satisfying IsNotFound if no snapshot has that ID.
func (c *Client) GetVMSnapshot(ctx context.Context, rid, snapshotID int) (*VMSnapshot, error) {
	snapshots, err := c.ListVMSnapshots(ctx, rid)
	if err != nil {
		return nil, err
	}
	for _, s := range snapshots {
		if s.ID == snapshotID {
			return &s, nil
		}
	}
	return nil, &apiError{StatusCode: 404, Body: fmt.Sprintf("snapshot id %d not found on VM rid %d", snapshotID, rid)}
}

// DeleteVMSnapshot removes a snapshot. DELETE /api/vm/snapshots/{rid}/{snapshotId}.
func (c *Client) DeleteVMSnapshot(ctx context.Context, rid, snapshotID int) error {
	return c.do(ctx, "DELETE", fmt.Sprintf("/api/vm/snapshots/%d/%d", rid, snapshotID), nil, nil)
}

// RollbackVMSnapshot restores a VM to a prior snapshot. POST
// /api/vm/snapshots/rollback/{rid}/{snapshotId} -- takes no request
// body; the handler hardcodes "destroy more recent snapshots/state" on
// the server side, there is no client-controllable option here.
func (c *Client) RollbackVMSnapshot(ctx context.Context, rid, snapshotID int) error {
	return c.do(ctx, "POST", fmt.Sprintf("/api/vm/snapshots/rollback/%d/%d", rid, snapshotID), nil, nil)
}
