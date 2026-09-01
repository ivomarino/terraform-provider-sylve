package sylveclient

import (
	"context"
	"fmt"
)

// ManualSwitch is Sylve's representation of a manual network switch --
// just a named bridge interface with no physical port bound to it (unlike
// a "standard" switch, which requires >=1 physical port and is NOT
// covered by this client yet: on a single-NIC host, standard-switch
// creation binds the box's only physical interface into a new bridge,
// which can sever the very connection managing it -- treated the same
// way this repo already treats the atlantis/baar gateway VMs. See the
// provider's dev notes.)
type ManualSwitch struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Bridge string `json:"bridge"`
}

type manualSwitchListEnvelope struct {
	Data struct {
		Manual []ManualSwitch `json:"manual"`
	} `json:"data"`
}

// CreateManualSwitch creates a manual switch. POST /api/network/manual-switch
// -- the response carries no id/body (Data: nil in the source), so the
// caller must list afterwards to learn it; see GetManualSwitchByName.
func (c *Client) CreateManualSwitch(ctx context.Context, name, bridge string) error {
	err := c.do(ctx, "POST", "/api/network/manual-switch",
		map[string]string{"name": name, "bridge": bridge}, nil)
	if err != nil {
		return fmt.Errorf("creating manual switch %q: %w", name, err)
	}
	return nil
}

// ListManualSwitches returns every manual switch. GET /api/network/switch
// returns {"standard": [...], "manual": [...]}; this only surfaces the
// manual half, matching what this client currently manages.
func (c *Client) ListManualSwitches(ctx context.Context) ([]ManualSwitch, error) {
	var out manualSwitchListEnvelope
	if err := c.do(ctx, "GET", "/api/network/switch", nil, &out); err != nil {
		return nil, err
	}
	return out.Data.Manual, nil
}

// GetManualSwitch finds a manual switch by ID. There is no per-ID GET
// endpoint (only the combined list), so this lists and filters.
// Returns an error satisfying IsNotFound if no switch has that ID.
func (c *Client) GetManualSwitch(ctx context.Context, id int) (*ManualSwitch, error) {
	switches, err := c.ListManualSwitches(ctx)
	if err != nil {
		return nil, err
	}
	for _, sw := range switches {
		if sw.ID == id {
			return &sw, nil
		}
	}
	return nil, &apiError{StatusCode: 404, Body: fmt.Sprintf("manual switch id %d not found", id)}
}

// GetManualSwitchByName finds a manual switch by name -- used right after
// CreateManualSwitch, whose response doesn't carry the new switch's ID.
func (c *Client) GetManualSwitchByName(ctx context.Context, name string) (*ManualSwitch, error) {
	switches, err := c.ListManualSwitches(ctx)
	if err != nil {
		return nil, err
	}
	for _, sw := range switches {
		if sw.Name == name {
			return &sw, nil
		}
	}
	return nil, &apiError{StatusCode: 404, Body: fmt.Sprintf("manual switch %q not found after create", name)}
}

// DeleteManualSwitch removes a manual switch. DELETE
// /api/network/manual-switch/{id}.
func (c *Client) DeleteManualSwitch(ctx context.Context, id int) error {
	return c.do(ctx, "DELETE", fmt.Sprintf("/api/network/manual-switch/%d", id), nil, nil)
}
