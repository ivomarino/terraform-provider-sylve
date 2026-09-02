package sylveclient

import (
	"context"
	"fmt"
)

// Jail is Sylve's representation of a FreeBSD/Linux jail, trimmed to the
// fields this provider manages. Structurally parallel to VM (see vm.go's
// own doc comment) -- CTID plays the same role RID does for VMs: a
// caller-chosen, required, 1-9999, unique identifier with NO server-side
// auto-assignment (confirmed against ValidateCreate,
// internal/services/jail/jail.go: `data.CTID == nil || *data.CTID <= 0 ||
// *data.CTID > 9999` => invalid_ct_id).
type Jail struct {
	// Prior to Sylve v0.3.0, SetJailName/SetJailDescription required the
	// jail's internal database primary key instead of CTID (unlike every
	// other jail/VM update endpoint, which all take the caller-facing
	// CTID/RID) -- this provider used to carry that extra id (as DBID)
	// purely to plumb it through to those two calls. Fixed upstream in
	// v0.3.0: both endpoints now take CTID like everything else, so
	// there's nothing left to carry.
	CTID        int    `json:"ctId"`
	Name        string `json:"name"`
	Hostname    string `json:"hostname,omitempty"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type"` // "freebsd" or "linux"

	Pool string `json:"pool,omitempty"`
	// Base is the UUID of an existing sylve_download whose utype is
	// "base-rootfs" -- REQUIRED (unlike a VM's optional iso), and it
	// must already be extracted (automaticExtraction on that download).
	// ValidateCreate rejects an empty Base outright with
	// download_uuid_required.
	Base string `json:"base,omitempty"`

	// SwitchName is REQUIRED (unlike a VM's optional switchName) --
	// ValidateCreate rejects an empty value with switch_name_required.
	// Pass "none" or "inherit" to opt out of a real switch attachment
	// (both skip the switch-lookup validation; "inherit" additionally
	// implies inheriting the host's own IPv4/IPv6, see InheritIPv4/6).
	SwitchName string `json:"switchName,omitempty"`

	Cores          int  `json:"cores"`
	Memory         int  `json:"memory"` // bytes, same convention as VM's RAM -- confirmed against hardware.go's own `jail.Memory = int(memoryBytes)` assignment
	ResourceLimits bool `json:"resourceLimits"`
	StartAtBoot    bool `json:"startAtBoot"`
	StartOrder     int  `json:"startOrder"`

	CleanEnvironment  bool   `json:"cleanEnvironment"`
	AdditionalOptions string `json:"additionalOptions,omitempty"`
	DevFSRuleset      string `json:"devfsRuleset,omitempty"`
	Fstab             string `json:"fstab,omitempty"`
	ResolvConf        string `json:"resolvConf,omitempty"`
}

type createJailRequest struct {
	Name        string `json:"name"`
	CTID        int    `json:"ctId"`
	Hostname    string `json:"hostname,omitempty"`
	Description string `json:"description,omitempty"`

	Pool       string `json:"pool"`
	Base       string `json:"base"`
	SwitchName string `json:"switchName"`

	ResourceLimits bool   `json:"resourceLimits"`
	Cores          int    `json:"cores,omitempty"`
	Memory         int    `json:"memory,omitempty"`
	StartAtBoot    bool   `json:"startAtBoot"`
	StartOrder     int    `json:"startOrder"`
	DevFSRuleset   string `json:"devfsRuleset,omitempty"`

	Type              string `json:"type"`
	CleanEnvironment  bool   `json:"cleanEnvironment"`
	AdditionalOptions string `json:"additionalOptions,omitempty"`
	Fstab             string `json:"fstab,omitempty"`
	ResolvConf        string `json:"resolvConf,omitempty"`
}

type jailEnvelope struct {
	Data Jail `json:"data"`
}

type jailListEnvelope struct {
	Data []Jail `json:"data"`
}

// CreateJail creates a new jail. Like CreateVM, the response carries no
// body, so this re-fetches by CTID afterwards.
func (c *Client) CreateJail(ctx context.Context, in Jail) (*Jail, error) {
	body := createJailRequest{
		Name:              in.Name,
		CTID:              in.CTID,
		Hostname:          in.Hostname,
		Description:       in.Description,
		Pool:              in.Pool,
		Base:              in.Base,
		SwitchName:        in.SwitchName,
		ResourceLimits:    in.ResourceLimits,
		Cores:             in.Cores,
		Memory:            in.Memory,
		StartAtBoot:       in.StartAtBoot,
		StartOrder:        in.StartOrder,
		DevFSRuleset:      in.DevFSRuleset,
		Type:              in.Type,
		CleanEnvironment:  in.CleanEnvironment,
		AdditionalOptions: in.AdditionalOptions,
		Fstab:             in.Fstab,
		ResolvConf:        in.ResolvConf,
	}
	if err := c.do(ctx, "POST", "/api/jail", body, nil); err != nil {
		return nil, fmt.Errorf("creating jail %q (ctid %d): %w", in.Name, in.CTID, err)
	}
	return c.GetJail(ctx, in.CTID)
}

// ListJails returns every jail on the node.
func (c *Client) ListJails(ctx context.Context) ([]Jail, error) {
	var out jailListEnvelope
	if err := c.do(ctx, "GET", "/api/jail", nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// GetJail fetches a jail by CTID (GetJailByIdentifier accepts either
// CTID or the internal DB id, same as VM's GetVMByIdentifier; this
// always passes CTID). Returns an error satisfying IsNotFound if it no
// longer exists.
func (c *Client) GetJail(ctx context.Context, ctid int) (*Jail, error) {
	var out jailEnvelope
	if err := c.do(ctx, "GET", fmt.Sprintf("/api/jail/%d", ctid), nil, &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// DeleteJail removes a jail and (optionally) its MAC objects and root
// filesystem. Like DeleteVM, both query params are mandatory with no
// default -- omitting either 400s with a missing_..._param error naming
// exactly that one. Path unchanged from v0.2.x (DELETE /api/jail/{ctid}),
// confirmed against v0.3.0 source (parseRequiredJailBoolQuery for both
// params, same as before).
func (c *Client) DeleteJail(ctx context.Context, ctid int) error {
	return c.do(ctx, "DELETE",
		fmt.Sprintf("/api/jail/%d?deletemacs=true&deleterootfs=true", ctid),
		nil, nil)
}

// SetJailName renames a jail. v0.3.0: PATCH /api/jail/{ctid}/name -- the
// CTID-vs-internal-DB-primary-key bug that made this take Jail.DBID in
// v0.2.x is fixed upstream (confirmed against source, jail.go:772:
// UpdateJailName now calls parseJailCTID(c, "ctid") directly, same as
// every other jail endpoint). Takes ctid now, not dbID -- DBID is gone
// from this client entirely, see Jail struct's own comment.
func (c *Client) SetJailName(ctx context.Context, ctid int, name string) error {
	return c.do(ctx, "PATCH", fmt.Sprintf("/api/jail/%d/name", ctid), map[string]any{"name": name}, nil)
}

// SetJailDescription updates a jail's description. v0.3.0: PATCH
// /api/jail/{ctid}/description -- same CTID fix as SetJailName.
func (c *Client) SetJailDescription(ctx context.Context, ctid int, description string) error {
	return c.do(ctx, "PATCH", fmt.Sprintf("/api/jail/%d/description", ctid), map[string]any{"description": description}, nil)
}

// SetJailCPU reconfigures a jail's core count. v0.3.0: PUT
// /api/jail/{ctid}/hardware/cpu (ctid moved from body to path; "cores"
// field name unchanged, confirmed against JailUpdateCPURequest).
func (c *Client) SetJailCPU(ctx context.Context, ctid int, cores int) error {
	return c.do(ctx, "PUT", fmt.Sprintf("/api/jail/%d/hardware/cpu", ctid), map[string]any{"cores": cores}, nil)
}

// SetJailMemory reconfigures a jail's memory limit in bytes. v0.3.0: PUT
// /api/jail/{ctid}/hardware/ram (ctid moved from body to path; "memory"
// field name unchanged, confirmed against JailUpdateMemoryRequest).
func (c *Client) SetJailMemory(ctx context.Context, ctid int, memoryBytes int) error {
	return c.do(ctx, "PUT", fmt.Sprintf("/api/jail/%d/hardware/ram", ctid), map[string]any{"memory": memoryBytes}, nil)
}

// JailAction is a lifecycle action queued via POST
// /api/jail/action/{action}/{ctId} -- action before ctid, same order as
// VMAction's real path.
type JailAction string

const (
	JailActionStart JailAction = "start"
	JailActionStop  JailAction = "stop"
)

// DoJailAction queues a lifecycle action (start/stop) for a jail.
func (c *Client) DoJailAction(ctx context.Context, ctid int, action JailAction) error {
	return c.do(ctx, "POST", fmt.Sprintf("/api/jail/action/%s/%d", action, ctid), nil, nil)
}
