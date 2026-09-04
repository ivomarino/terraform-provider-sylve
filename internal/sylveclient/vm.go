package sylveclient

import (
	"context"
	"fmt"
	"time"
)

// VM is Sylve's representation of a bhyve virtual machine, trimmed to the
// fields this provider currently manages. See
// https://sylve.io/api-reference/operations/vm/post/ for the full shape,
// or (more reliably -- the swagger spec undercounts some required-ness,
// see the provider's own dev notes) the source's
// internal/db/models/vm.VM and internal/interfaces/services/libvirt.CreateVMRequest.
//
// RAM is in BYTES, not MiB -- confirmed against the service's own minimum
// check (data.RAM < 1024*1024*128) and against a live VM's returned
// value. An earlier version of this client got this wrong.
type VM struct {
	RID         int    `json:"rid"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	RAM         int64  `json:"ram"` // bytes
	CPUCores    int    `json:"cpuCores"`
	CPUSockets  int    `json:"cpuSockets"`
	CPUThreads  int    `json:"cpuThreads"`
	TimeOffset  string `json:"timeOffset"` // "utc" or "localtime"

	VNCPort       int    `json:"vncPort"`
	VNCPassword   string `json:"vncPassword,omitempty"`
	VNCResolution string `json:"vncResolution,omitempty"`
	// VNCWait is genuinely dangerous to leave unset: the create service
	// defaults a nil/omitted VNCWait to TRUE (pauses the guest CPU until
	// a VNC client connects -- confirmed live, 2026-09-01, a VM created
	// without this field sat at ~0s CPU time indefinitely, looking
	// "Running" in the domain status while never actually booting; see
	// the provider's dev notes). CreateVM always sends this explicitly
	// (see the *bool in createVMRequest) so a VM never silently inherits
	// that default. Unlike StoragePool/SwitchName/ISO, this one IS
	// returned by GET /vm/:id, so it's read back for real.
	VNCWait bool `json:"vncWait"`

	Serial         bool `json:"serial"`
	TPMEmulation   bool `json:"tpmEmulation"`
	QemuGuestAgent bool `json:"qemuGuestAgent"`
	StartAtBoot    bool `json:"startAtBoot"`
	StartOrder     int  `json:"startOrder"`

	// First-disk / first-NIC convenience fields, set only at create time
	// (via the fields of the same name below) -- Sylve's own create
	// endpoint accepts these inline, but its read response nests the
	// resulting disk/NIC under storages[]/networks[] instead of echoing
	// these back flat. sylve_vm itself does not read those nested lists
	// back (see that resource's own doc comment), so these fields are
	// write-only from ITS perspective: set once at create, never
	// refreshed, and any change requires replacement. Storages below IS
	// used, by sylve_vm_storage's Read/lookup logic.
	StoragePool          string `json:"storagePool,omitempty"`
	StorageType          string `json:"storageType,omitempty"`
	StorageSize          uint64 `json:"storageSize,omitempty"`
	StorageEmulationType string `json:"storageEmulationType,omitempty"`
	SwitchName           string `json:"switchName,omitempty"`
	SwitchEmulationType  string `json:"switchEmulationType,omitempty"`

	// Storages is GET /vm/:id's own nested view of every disk/CD-ROM
	// attached to this VM -- see VMStorage's own doc comment (vm_storage.go).
	Storages []VMStorage `json:"storages,omitempty"`
	MacID    int         `json:"macId,omitempty"`

	// ISO is the UUID of a sylve_download to boot from / use as
	// cloud-init source, same write-only-at-create treatment as the
	// storage/switch fields above (not present in GET /vm/:id's flat
	// response).
	ISO string `json:"iso,omitempty"`

	// CloudInitData/MetaData/NetworkConfig ARE persisted and returned by
	// GET /vm/:id (see the DB model), unlike the create-only fields
	// above -- confirmed against internal/db/models/vm/vm.go, which has
	// no boolean "CloudInit" column at all: whether cloud-init is
	// "enabled" is a request-only concept used purely for validation
	// branching at create time (does the ISO need to resolve to a
	// cloud-init-capable download or not), never stored. This provider
	// exposes the enable flag anyway (see CreateVM below) purely to
	// drive that create-time validation choice, but there's nothing to
	// read back for it afterward -- it's genuinely gone once the VM
	// exists, in Sylve's own data model, not just in what this client
	// happens to expose.
	CloudInitData          string `json:"cloudInitData,omitempty"`
	CloudInitMetaData      string `json:"cloudInitMetaData,omitempty"`
	CloudInitNetworkConfig string `json:"cloudInitNetworkConfig,omitempty"`

	// CloudInit is the create-time-only enable flag described above --
	// write-only, like StoragePool/StorageType/SwitchName/ISO.
	CloudInit bool `json:"-"`
}

// createVMRequest mirrors libvirtServiceInterfaces.CreateVMRequest field
// names/JSON tags exactly (sylve-api-reference/sylve-src @ v0.2.3,
// internal/interfaces/services/libvirt/vm.go). Required by the real API:
// name, rid (0 = auto-assign), cpuSockets, cpuCores, cpuThreads, ram,
// vncPort (must be non-zero), timeOffset. Everything else is optional.
type createVMRequest struct {
	Name        string `json:"name"`
	RID         int    `json:"rid"`
	Description string `json:"description,omitempty"`

	StoragePool          string `json:"storagePool,omitempty"`
	StorageType          string `json:"storageType,omitempty"`
	StorageSize          uint64 `json:"storageSize,omitempty"`
	StorageEmulationType string `json:"storageEmulationType,omitempty"`

	SwitchName          string `json:"switchName,omitempty"`
	SwitchEmulationType string `json:"switchEmulationType,omitempty"`
	MacId               int    `json:"macId,omitempty"`

	ISO string `json:"iso,omitempty"`

	CPUSockets int   `json:"cpuSockets"`
	CPUCores   int   `json:"cpuCores"`
	CPUThreads int   `json:"cpuThreads"`
	RAM        int64 `json:"ram"`

	VNCPort       int    `json:"vncPort"`
	VNCPassword   string `json:"vncPassword,omitempty"`
	VNCResolution string `json:"vncResolution,omitempty"`
	// *bool, not bool -- must always be sent non-nil (see VNCWait's own
	// doc comment on the VM struct above for why omitting it is
	// dangerous).
	VNCWait *bool `json:"vncWait"`

	Serial         bool   `json:"serial"`
	TPMEmulation   bool   `json:"tpmEmulation"`
	QemuGuestAgent bool   `json:"qemuGuestAgent"`
	StartAtBoot    bool   `json:"startAtBoot"`
	StartOrder     int    `json:"startOrder"`
	TimeOffset     string `json:"timeOffset"`

	CloudInit              *bool  `json:"cloudInit,omitempty"`
	CloudInitData          string `json:"cloudInitData,omitempty"`
	CloudInitMetaData      string `json:"cloudInitMetaData,omitempty"`
	CloudInitNetworkConfig string `json:"cloudInitNetworkConfig,omitempty"`
}

// vmEnvelope wraps a single-VM API response. CreateVM's own response is
// looser than Read's (the service returns `any`, per swagger) so the
// client falls back to re-fetching by RID after create -- see CreateVM.
type vmEnvelope struct {
	Data VM `json:"data"`
}

type vmListEnvelope struct {
	Data []VM `json:"data"`
}

// CreateVM creates a new VM. in.RID must be a caller-chosen value in
// 1-9999, unique across VMs -- Sylve's validateCreate rejects rid <= 0
// outright with invalid_rid; there is no server-side auto-assignment
// despite what some docs imply (confirmed live, 2026-08-31).
//
// Sylve's own POST /vm response does not reliably echo the created VM
// back in the same shape as GET /vm/:id (its swagger-declared response
// type is just `any`), so this re-fetches by RID after a successful
// create rather than trusting the create response's body.
func (c *Client) CreateVM(ctx context.Context, in VM) (*VM, error) {
	body := createVMRequest{
		Name:                   in.Name,
		RID:                    in.RID,
		Description:            in.Description,
		StoragePool:            in.StoragePool,
		StorageType:            in.StorageType,
		StorageSize:            in.StorageSize,
		StorageEmulationType:   in.StorageEmulationType,
		SwitchName:             in.SwitchName,
		SwitchEmulationType:    in.SwitchEmulationType,
		MacId:                  in.MacID,
		ISO:                    in.ISO,
		CPUSockets:             in.CPUSockets,
		CPUCores:               in.CPUCores,
		CPUThreads:             in.CPUThreads,
		RAM:                    in.RAM,
		VNCPort:                in.VNCPort,
		VNCPassword:            in.VNCPassword,
		VNCResolution:          in.VNCResolution,
		Serial:                 in.Serial,
		TPMEmulation:           in.TPMEmulation,
		QemuGuestAgent:         in.QemuGuestAgent,
		StartAtBoot:            in.StartAtBoot,
		StartOrder:             in.StartOrder,
		TimeOffset:             in.TimeOffset,
		CloudInitData:          in.CloudInitData,
		CloudInitMetaData:      in.CloudInitMetaData,
		CloudInitNetworkConfig: in.CloudInitNetworkConfig,
	}
	if in.CloudInit {
		body.CloudInit = &in.CloudInit
	}
	// Always sent explicitly, non-nil -- see VNCWait's own doc comment.
	body.VNCWait = &in.VNCWait

	if err := c.do(ctx, "POST", "/api/vm", body, nil); err != nil {
		return nil, fmt.Errorf("creating VM %q (rid %d): %w", in.Name, in.RID, err)
	}
	return c.GetVM(ctx, in.RID)
}

// ListVMs returns every VM on the node.
func (c *Client) ListVMs(ctx context.Context) ([]VM, error) {
	var out vmListEnvelope
	if err := c.do(ctx, "GET", "/api/vm", nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// GetVM fetches a VM by its resource ID (Sylve's GetVMByIdentifier accepts
// either RID or the internal DB id; this always passes RID). Returns an
// error satisfying IsNotFound if it no longer exists.
func (c *Client) GetVM(ctx context.Context, rid int) (*VM, error) {
	var out vmEnvelope
	err := c.do(ctx, "GET", fmt.Sprintf("/api/vm/%d", rid), nil, &out)
	if err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// DeleteVM removes a VM and everything it owns: MAC network-objects, raw
// disk files, and ZFS volumes. The non-force delete path 400s outright if
// ANY of deletemacs/deleterawdisks/deletevolumes is missing from the
// query string -- none of the three default to a value, all three are
// mandatory. Path unchanged from v0.2.x (DELETE /api/vm/{rid}) --
// v0.3.0 added a separate `force=true` shortcut with different (fewer)
// required params plus a new registration-purge endpoint, neither used
// here; confirmed the non-force path this client relies on still takes
// the identical three params by reading RemoveVM directly, not assumed
// from the changelog. Sylve's API 404s if the VM is already gone;
// callers that want delete-is-idempotent semantics should check
// IsNotFound.
func (c *Client) DeleteVM(ctx context.Context, rid int) error {
	return c.do(ctx, "DELETE",
		fmt.Sprintf("/api/vm/%d?deletemacs=true&deleterawdisks=true&deletevolumes=true", rid),
		nil, nil)
}

// SetVMName renames a VM. v0.3.0: PATCH /api/vm/{rid}/name -- rid moved
// from the body into the URL (v0.2.x was PUT /api/vm/name with rid in
// the body); "name" field itself unchanged.
func (c *Client) SetVMName(ctx context.Context, rid int, name string) error {
	return c.do(ctx, "PATCH", fmt.Sprintf("/api/vm/%d/name", rid), map[string]any{"name": name}, nil)
}

// SetVMDescription updates a VM's description. v0.3.0: PATCH
// /api/vm/{rid}/description, same rid-into-URL move as SetVMName.
func (c *Client) SetVMDescription(ctx context.Context, rid int, description string) error {
	return c.do(ctx, "PATCH", fmt.Sprintf("/api/vm/%d/description", rid), map[string]any{"description": description}, nil)
}

type cpuConfig struct {
	CPUSockets int   `json:"cpuSockets"`
	CPUCores   int   `json:"cpuCores"`
	CPUThreads int   `json:"cpuThreads"`
	CPUPinning []any `json:"cpuPinning"`
}

// SetVMCPU reconfigures a VM's CPU topology. v0.3.0: PUT
// /api/vm/{rid}/hardware/cpu -- rid moved from a path-suffix
// (/api/vm/hardware/cpu/{rid} in v0.2.x) to a path-prefix, same
// direction as every other rid-bearing endpoint in this file. Body
// fields (cpuSockets/cpuCores/cpuThreads/cpuPinning) unchanged.
func (c *Client) SetVMCPU(ctx context.Context, rid, cores, sockets, threads int) error {
	return c.do(ctx, "PUT", fmt.Sprintf("/api/vm/%d/hardware/cpu", rid), cpuConfig{
		CPUSockets: sockets, CPUCores: cores, CPUThreads: threads, CPUPinning: []any{},
	}, nil)
}

// SetVMRAM reconfigures a VM's memory in bytes. v0.3.0: PUT
// /api/vm/{rid}/hardware/ram -- same rid-to-prefix move as SetVMCPU.
func (c *Client) SetVMRAM(ctx context.Context, rid int, ramBytes int64) error {
	return c.do(ctx, "PUT", fmt.Sprintf("/api/vm/%d/hardware/ram", rid), map[string]int64{"ram": ramBytes}, nil)
}

// SetVMTimeOffset reconfigures a VM's RTC ("utc" or "localtime"). v0.3.0:
// PUT /api/vm/{rid}/options/clock.
func (c *Client) SetVMTimeOffset(ctx context.Context, rid int, timeOffset string) error {
	return c.do(ctx, "PUT", fmt.Sprintf("/api/vm/%d/options/clock", rid), map[string]string{"timeOffset": timeOffset}, nil)
}

// SetVMTPM enables/disables TPM emulation. v0.3.0: PUT
// /api/vm/{rid}/options/tpm.
func (c *Client) SetVMTPM(ctx context.Context, rid int, enabled bool) error {
	return c.do(ctx, "PUT", fmt.Sprintf("/api/vm/%d/options/tpm", rid), map[string]bool{"enabled": enabled}, nil)
}

// SetVMQemuGuestAgent enables/disables the QEMU guest agent channel.
// v0.3.0: PUT /api/vm/{rid}/options/qemu-guest-agent.
func (c *Client) SetVMQemuGuestAgent(ctx context.Context, rid int, enabled bool) error {
	return c.do(ctx, "PUT", fmt.Sprintf("/api/vm/%d/options/qemu-guest-agent", rid), map[string]bool{"enabled": enabled}, nil)
}

// SetVMSerialConsole enables/disables the serial console. v0.3.0: PUT
// /api/vm/{rid}/options/serial-console.
func (c *Client) SetVMSerialConsole(ctx context.Context, rid int, enabled bool) error {
	return c.do(ctx, "PUT", fmt.Sprintf("/api/vm/%d/options/serial-console", rid), map[string]bool{"enabled": enabled}, nil)
}

// SetVMBootOrder updates start-at-boot and boot-order together -- both
// fields travel in one request (ModifyBootOrderRequest), not two
// separate endpoints. v0.3.0: PUT /api/vm/{rid}/options/boot-order.
func (c *Client) SetVMBootOrder(ctx context.Context, rid int, startAtBoot bool, bootOrder int) error {
	return c.do(ctx, "PUT", fmt.Sprintf("/api/vm/%d/options/boot-order", rid),
		map[string]any{"startAtBoot": startAtBoot, "bootOrder": bootOrder}, nil)
}

// QGAOSInfo is the guest OS info reported by the QEMU guest agent.
type QGAOSInfo struct {
	Name          string `json:"name"`
	KernelRelease string `json:"kernel-release"`
	Version       string `json:"version"`
	PrettyName    string `json:"pretty-name"`
	VersionID     string `json:"version-id"`
	KernelVersion string `json:"kernel-version"`
	Machine       string `json:"machine"`
	ID            string `json:"id"`
}

// QGANetworkInterface is one guest-reported network interface.
type QGANetworkInterface struct {
	Name            string `json:"name"`
	HardwareAddress string `json:"hardware-address"`
	IPAddresses     []struct {
		Type    string `json:"ip-address-type"`
		Address string `json:"ip-address"`
		Prefix  int    `json:"prefix"`
	} `json:"ip-addresses"`
}

// QGAInfo is the full response from the QEMU guest agent channel --
// live telemetry, not managed state; confirmed live, 2026-09-01, this
// is genuinely different information than anything sylve_vm's own
// Read() surfaces (it comes from inside the guest OS itself, via the
// virtio-console channel, independent of the VM object's own DB row).
type QGAInfo struct {
	OSInfo     QGAOSInfo             `json:"osInfo"`
	Interfaces []QGANetworkInterface `json:"interfaces"`
}

// GetQGAInfo queries the QEMU guest agent for live guest info. GET
// /api/vm/qga/{rid}. Requires: qemu_guest_agent enabled on the VM at
// create time, the guest agent package actually installed and running
// inside the guest (nothing installs it automatically -- see
// sylve_vm's cloud_init attributes for one way to get it there), and
// the VM actually running. Times out server-side
// (failed_to_decode_qga_response: read unix ->qga.sock: i/o timeout) if
// the agent isn't there to answer -- confirmed live chasing what turned
// out to be a completely unrelated cloud-init networking bug, see the
// provider's dev notes.
func (c *Client) GetQGAInfo(ctx context.Context, rid int) (*QGAInfo, error) {
	var out struct {
		Data QGAInfo `json:"data"`
	}
	// v0.3.0: GET /api/vm/{rid}/guest-agent -- renamed outright from
	// /api/vm/qga/{rid} (not just an rid-position move like most of this
	// file's other endpoints), confirmed against the real route table.
	if err := c.do(ctx, "GET", fmt.Sprintf("/api/vm/%d/guest-agent", rid), nil, &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// SetVMCloudInit updates a VM's cloud-init user-data/meta-data/network-
// config. PUT /api/vm/options/cloud-init/{rid}. Note this can only ever
// change the DATA, not "enable" cloud-init on a VM that wasn't created
// with it -- there is no persisted enable flag to flip (see CloudInit's
// own doc comment on the VM struct).
func (c *Client) SetVMCloudInit(ctx context.Context, rid int, data, metadata, networkConfig string) error {
	// v0.3.0: PUT /api/vm/{rid}/options/cloud-init -- rid moved to prefix,
	// body fields (data/metadata/networkConfig) unchanged.
	return c.do(ctx, "PUT", fmt.Sprintf("/api/vm/%d/options/cloud-init", rid), map[string]string{
		"data": data, "metadata": metadata, "networkConfig": networkConfig,
	}, nil)
}

// VMAction is a lifecycle action queued via POST /vm/{rid}/actions/{action}
// (v0.3.0). v0.2.x had this the other way round --
// POST /vm/{action}/{rid}, action first -- which was itself a correction
// from an even earlier guess at a RESTful /vm/{rid}/actions/{action}
// shape that didn't exist in v0.2.x. v0.3.0 genuinely moved TO that
// RESTful shape -- confirmed via source, not assumed from the pattern
// repeating.
type VMAction string

const (
	VMActionStart VMAction = "start"
	VMActionStop  VMAction = "stop"
)

// DoVMAction queues a lifecycle action (start/stop) for a VM. The
// response only confirms the action was queued ("vm_action_queued"),
// not that it completed -- see WaitForDomainStatus to actually wait for
// the transition.
func (c *Client) DoVMAction(ctx context.Context, rid int, action VMAction) error {
	return c.do(ctx, "POST", fmt.Sprintf("/api/vm/%d/actions/%s", rid, action), nil, nil)
}

// GetDomainStatus returns the VM's real, live libvirt/bhyve domain
// status (e.g. "Running", "Shutoff") -- GET /api/vm/domain/{rid}. This
// is the authoritative live state; the VM object's own "state" field
// (returned by GetVM) is a different, less immediately reliable concept
// -- confirmed live, 2026-09-01: a VM stuck paused waiting for a VNC
// connection (see VNCWait's own doc comment) still reported domain
// status "Running" despite never having executed a single guest
// instruction, so "Running" here means "the bhyve process exists and is
// not shut off," not "the guest OS is up."
func (c *Client) GetDomainStatus(ctx context.Context, rid int) (string, error) {
	var out struct {
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	// v0.3.0: GET /api/vm/{rid}/domain -- rid moved to prefix (was
	// /api/vm/domain/{rid}).
	//
	// FOUND LIVE: a multi-host deployment can genuinely have some hosts
	// still on a pre-v0.3.0 Sylve build while others have been upgraded --
	// the old hosts only serve the pre-migration path; hitting the new
	// path there 404s through to Sylve's own SPA (returns index.html, not
	// JSON, hence the "invalid character '<'" decode error this used to
	// surface verbatim). Rather than pin this whole client to one API
	// generation, try the new path first and fall back to the old one on
	// ANY failure from it -- cheap (worst case one extra round-trip per
	// call, only on hosts that actually need it) and self-adapting per-host
	// rather than needing a version flag threaded through every caller.
	if err := c.do(ctx, "GET", fmt.Sprintf("/api/vm/%d/domain", rid), nil, &out); err == nil {
		return out.Data.Status, nil
	}
	if err := c.do(ctx, "GET", fmt.Sprintf("/api/vm/domain/%d", rid), nil, &out); err != nil {
		return "", err
	}
	return out.Data.Status, nil
}

// WaitForDomainStatus polls GetDomainStatus until it equals want, or
// timeout elapses.
func (c *Client) WaitForDomainStatus(ctx context.Context, rid int, want string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		status, err := c.GetDomainStatus(ctx, rid)
		if err == nil && status == want {
			return nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return fmt.Errorf("VM rid %d did not reach domain status %q within %s (last error: %w)", rid, want, timeout, err)
			}
			return fmt.Errorf("VM rid %d did not reach domain status %q within %s (last status %q)", rid, want, timeout, status)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}
