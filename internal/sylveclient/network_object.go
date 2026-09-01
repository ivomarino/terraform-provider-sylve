package sylveclient

import (
	"context"
	"fmt"
)

// NetworkObject is Sylve's generic "named set of values" primitive --
// backs MAC addresses, IPs/CIDRs, hostnames, ports, countries, and lists
// of any of the above, all through the same table. VM/jail `mac_id` (not
// yet wired into this provider) and switch `network4`/`gateway4` etc.
// reference one of these by ID.
type NetworkObject struct {
	ID      int      `json:"id"`
	Name    string   `json:"name"`
	Type    string   `json:"type"` // "Host", "Mac", "Network", "Port", "Country", or "List"
	Comment string   `json:"description"`
	Values  []string `json:"-"` // flattened from Entries below by this client, not part of the wire format directly
	Entries []struct {
		Value string `json:"value"`
	} `json:"entries"`
}

type networkObjectCreateResponse struct {
	Data int `json:"data"`
}

type networkObjectListEnvelope struct {
	Data []NetworkObject `json:"data"`
}

type networkObjectRequest struct {
	Name   string   `json:"name"`
	Type   string   `json:"type"`
	Values []string `json:"values"`
}

// CreateNetworkObject creates a network object and returns its new ID --
// unlike most create endpoints in this provider, this one actually
// returns it directly rather than requiring a follow-up list/lookup.
func (c *Client) CreateNetworkObject(ctx context.Context, name, objType string, values []string) (int, error) {
	var out networkObjectCreateResponse
	err := c.do(ctx, "POST", "/api/network/object",
		networkObjectRequest{Name: name, Type: objType, Values: values}, &out)
	if err != nil {
		return 0, fmt.Errorf("creating network object %q: %w", name, err)
	}
	return out.Data, nil
}

// EditNetworkObject replaces a network object's name/type/values in
// full. PUT /api/network/object/{id}.
func (c *Client) EditNetworkObject(ctx context.Context, id int, name, objType string, values []string) error {
	return c.do(ctx, "PUT", fmt.Sprintf("/api/network/object/%d", id),
		networkObjectRequest{Name: name, Type: objType, Values: values}, nil)
}

// DeleteNetworkObject removes a network object by ID.
func (c *Client) DeleteNetworkObject(ctx context.Context, id int) error {
	return c.do(ctx, "DELETE", fmt.Sprintf("/api/network/object/%d", id), nil, nil)
}

// ListNetworkObjects returns every network object on the node.
func (c *Client) ListNetworkObjects(ctx context.Context) ([]NetworkObject, error) {
	var out networkObjectListEnvelope
	if err := c.do(ctx, "GET", "/api/network/object", nil, &out); err != nil {
		return nil, err
	}
	for i := range out.Data {
		for _, e := range out.Data[i].Entries {
			out.Data[i].Values = append(out.Data[i].Values, e.Value)
		}
	}
	return out.Data, nil
}

// GetNetworkObject finds a network object by ID. There is no per-ID GET
// endpoint (only the combined list), so this lists and filters. Returns
// an error satisfying IsNotFound if no object has that ID.
func (c *Client) GetNetworkObject(ctx context.Context, id int) (*NetworkObject, error) {
	objects, err := c.ListNetworkObjects(ctx)
	if err != nil {
		return nil, err
	}
	for _, o := range objects {
		if o.ID == id {
			return &o, nil
		}
	}
	return nil, &apiError{StatusCode: 404, Body: fmt.Sprintf("network object id %d not found", id)}
}
