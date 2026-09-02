package sylveclient

import (
	"context"
	"fmt"
)

// VMNetwork is one NIC attached to a VM, as nested under GET /vm/:id's
// own networks[] array -- there is no per-NIC GET endpoint, only this
// nested view and the mutation endpoints (attach/update/detach). Unlike
// VMStorage, neither attach/detach/update requires the VM to be stopped
// first (no shutoff check found in source) -- NIC hot-plug, unlike disk
// attach.
type VMNetwork struct {
	ID         int    `json:"id"`
	MacID      int    `json:"macId"`
	SwitchID   int    `json:"switchId"`
	SwitchType string `json:"switchType"` // "manual" or "standard"
	Emulation  string `json:"emulation"`  // e.g. "virtio" -- NOT the same value space as storage emulation
	SwitchName string `json:"-"`          // resolved client-side from manualSwitch.name/standardSwitch.name, see decode below
}

// vmNetworkWire mirrors the raw nested shape (manualSwitch/standardSwitch
// as separate optional sub-objects) before being flattened into VMNetwork.
type vmNetworkWire struct {
	ID           int    `json:"id"`
	MacID        int    `json:"macId"`
	SwitchID     int    `json:"switchId"`
	SwitchType   string `json:"switchType"`
	Emulation    string `json:"emulation"`
	ManualSwitch *struct {
		Name string `json:"name"`
	} `json:"manualSwitch"`
	StandardSwitch *struct {
		Name string `json:"name"`
	} `json:"standardSwitch"`
}

func (w vmNetworkWire) toVMNetwork() VMNetwork {
	n := VMNetwork{ID: w.ID, MacID: w.MacID, SwitchID: w.SwitchID, SwitchType: w.SwitchType, Emulation: w.Emulation}
	if w.ManualSwitch != nil {
		n.SwitchName = w.ManualSwitch.Name
	} else if w.StandardSwitch != nil {
		n.SwitchName = w.StandardSwitch.Name
	}
	return n
}

// AttachNetwork attaches a NIC to a VM. POST /api/vm/network/attach. The
// response carries no ID, so callers use FindVMNetwork afterward to
// locate the new entry -- there's no name to match on the way
// FindVMStorageByName does, so the match is best-effort: the most
// recently created (highest ID) network entry on this VM whose
// switchName+emulation match what was requested. Good enough in
// practice (a VM rarely attaches two identical NICs back to back), but
// worth knowing if this ever mismatches on a VM with many NICs attached
// in rapid succession.
func (c *Client) AttachNetwork(ctx context.Context, rid int, switchName, emulation string, macID int) error {
	// v0.3.0: POST /api/vm/{rid}/networks -- rid moved to the URL path
	// (was in the body, POST /api/vm/network/attach, in v0.2.x); other
	// field names unchanged.
	body := map[string]any{"switchName": switchName, "emulation": emulation}
	if macID > 0 {
		body["macId"] = macID
	}
	if err := c.do(ctx, "POST", fmt.Sprintf("/api/vm/%d/networks", rid), body, nil); err != nil {
		return fmt.Errorf("attaching network (switch %q) to VM rid %d: %w", switchName, rid, err)
	}
	return nil
}

// UpdateNetwork changes an attached NIC's switch/emulation/MAC. v0.3.0:
// PATCH /api/vm/{rid}/networks/{networkId} -- now needs BOTH the VM's
// rid and the network's own ID in the path (v0.2.x only needed the
// network ID, PUT /api/vm/network/update, id-in-body). rid is a new
// required parameter on this method as of this compat pass.
func (c *Client) UpdateNetwork(ctx context.Context, rid, networkID int, switchName, emulation string, macID int) error {
	body := map[string]any{"switchName": switchName, "emulation": emulation}
	if macID > 0 {
		body["macId"] = macID
	}
	return c.do(ctx, "PATCH", fmt.Sprintf("/api/vm/%d/networks/%d", rid, networkID), body, nil)
}

// DetachNetwork removes a NIC from a VM. v0.3.0: DELETE
// /api/vm/{rid}/networks/{networkId}, empty body -- both path params now
// (was POST /api/vm/network/detach with both in the body).
func (c *Client) DetachNetwork(ctx context.Context, rid, networkID int) error {
	return c.do(ctx, "DELETE", fmt.Sprintf("/api/vm/%d/networks/%d", rid, networkID), nil, nil)
}

// getVMNetworks fetches the raw networks[] view for a VM and flattens it.
func (c *Client) getVMNetworks(ctx context.Context, rid int) ([]VMNetwork, error) {
	var out struct {
		Data struct {
			Networks []vmNetworkWire `json:"networks"`
		} `json:"data"`
	}
	if err := c.do(ctx, "GET", fmt.Sprintf("/api/vm/%d", rid), nil, &out); err != nil {
		return nil, err
	}
	result := make([]VMNetwork, len(out.Data.Networks))
	for i, w := range out.Data.Networks {
		result[i] = w.toVMNetwork()
	}
	return result, nil
}

// GetVMNetwork finds one NIC by its own ID. Returns an error satisfying
// IsNotFound if the VM or the specific NIC no longer exists.
func (c *Client) GetVMNetwork(ctx context.Context, rid, networkID int) (*VMNetwork, error) {
	networks, err := c.getVMNetworks(ctx, rid)
	if err != nil {
		return nil, err
	}
	for _, n := range networks {
		if n.ID == networkID {
			return &n, nil
		}
	}
	return nil, &apiError{StatusCode: 404, Body: fmt.Sprintf("network id %d not found on VM rid %d", networkID, rid)}
}

// FindVMNetwork finds a NIC by best-effort match -- see AttachNetwork's
// own doc comment for the matching caveat.
func (c *Client) FindVMNetwork(ctx context.Context, rid int, switchName, emulation string) (*VMNetwork, error) {
	networks, err := c.getVMNetworks(ctx, rid)
	if err != nil {
		return nil, err
	}
	var best *VMNetwork
	for i, n := range networks {
		if n.SwitchName == switchName && n.Emulation == emulation {
			if best == nil || n.ID > best.ID {
				best = &networks[i]
			}
		}
	}
	if best == nil {
		return nil, &apiError{StatusCode: 404, Body: fmt.Sprintf("no network matching switch %q found on VM rid %d after attach", switchName, rid)}
	}
	return best, nil
}
