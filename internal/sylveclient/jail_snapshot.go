package sylveclient

import (
	"context"
	"fmt"
)

// JailSnapshot is a point-in-time ZFS snapshot of a jail's root dataset
// -- structurally parallel to VMSnapshot, with one shape difference:
// RootDataset is a single string here, not a []string (a jail has one
// root dataset; a VM can have several).
type JailSnapshot struct {
	ID               int    `json:"id"`
	JailID           int    `json:"jid"`
	CTID             int    `json:"ctId"`
	ParentSnapshotID *int   `json:"parentSnapshotId"`
	Name             string `json:"name"`
	Description      string `json:"description"`
	SnapshotName     string `json:"snapshotName"`
	RootDataset      string `json:"rootDataset"`
}

type jailSnapshotEnvelope struct {
	Data JailSnapshot `json:"data"`
}

type jailSnapshotListEnvelope struct {
	Data []JailSnapshot `json:"data"`
}

// CreateJailSnapshot takes a snapshot of a jail by CTID. Returns the
// created object directly, same as CreateVMSnapshot.
func (c *Client) CreateJailSnapshot(ctx context.Context, ctid int, name, description string) (*JailSnapshot, error) {
	var out jailSnapshotEnvelope
	err := c.do(ctx, "POST", fmt.Sprintf("/api/jail/snapshots/%d", ctid),
		map[string]string{"name": name, "description": description}, &out)
	if err != nil {
		return nil, fmt.Errorf("creating snapshot %q of jail ctid %d: %w", name, ctid, err)
	}
	return &out.Data, nil
}

// ListJailSnapshots returns every snapshot for a jail by CTID.
func (c *Client) ListJailSnapshots(ctx context.Context, ctid int) ([]JailSnapshot, error) {
	var out jailSnapshotListEnvelope
	if err := c.do(ctx, "GET", fmt.Sprintf("/api/jail/snapshots/%d", ctid), nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// GetJailSnapshot finds a snapshot by its own ID. No per-snapshot GET
// endpoint, only the per-jail list. Returns an error satisfying
// IsNotFound if no snapshot has that ID.
func (c *Client) GetJailSnapshot(ctx context.Context, ctid, snapshotID int) (*JailSnapshot, error) {
	snapshots, err := c.ListJailSnapshots(ctx, ctid)
	if err != nil {
		return nil, err
	}
	for _, s := range snapshots {
		if s.ID == snapshotID {
			return &s, nil
		}
	}
	return nil, &apiError{StatusCode: 404, Body: fmt.Sprintf("snapshot id %d not found on jail ctid %d", snapshotID, ctid)}
}

// DeleteJailSnapshot removes a snapshot. DELETE /api/jail/snapshots/{ctid}/{snapshotId}.
func (c *Client) DeleteJailSnapshot(ctx context.Context, ctid, snapshotID int) error {
	return c.do(ctx, "DELETE", fmt.Sprintf("/api/jail/snapshots/%d/%d", ctid, snapshotID), nil, nil)
}

// RollbackJailSnapshot restores a jail to a prior snapshot. POST
// /api/jail/snapshots/rollback/{ctid}/{snapshotId} -- no request body;
// same "server hardcodes destroying newer state" shape as
// RollbackVMSnapshot.
func (c *Client) RollbackJailSnapshot(ctx context.Context, ctid, snapshotID int) error {
	return c.do(ctx, "POST", fmt.Sprintf("/api/jail/snapshots/rollback/%d/%d", ctid, snapshotID), nil, nil)
}
