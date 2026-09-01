package provider

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ivomarino/terraform-provider-sylve/internal/sylveclient"
)

var (
	_ resource.Resource                = &vmResource{}
	_ resource.ResourceWithConfigure   = &vmResource{}
	_ resource.ResourceWithImportState = &vmResource{}
)

// NewVMResource is the constructor registered in provider.Resources.
func NewVMResource() resource.Resource {
	return &vmResource{}
}

type vmResource struct {
	client *sylveclient.Client
}

// vmResourceModel is sylve_vm's Terraform-side schema. RID is computed
// (Sylve assigns it) unless the practitioner deliberately pins one.
//
// storage_pool/storage_type/storage_size/storage_emulation_type/
// switch_name/mac_id are write-only-at-create: Sylve's create endpoint
// accepts them inline for the VM's first disk/NIC, but its read response
// nests the result under storages[]/networks[] instead of echoing these
// back flat, so this resource can't verify them after creation. Changing
// any of them forces recreation rather than silently doing nothing.
type vmResourceModel struct {
	ID          types.String `tfsdk:"id"` // string form of RID, for Terraform's own resource identity
	RID         types.Int64  `tfsdk:"rid"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	RAM         types.Int64  `tfsdk:"ram"`
	CPUCores    types.Int64  `tfsdk:"cpu_cores"`
	CPUSockets  types.Int64  `tfsdk:"cpu_sockets"`
	CPUThreads  types.Int64  `tfsdk:"cpu_threads"`
	TimeOffset  types.String `tfsdk:"time_offset"`

	VNCPort       types.Int64  `tfsdk:"vnc_port"`
	VNCPassword   types.String `tfsdk:"vnc_password"`
	VNCResolution types.String `tfsdk:"vnc_resolution"`
	VNCWait       types.Bool   `tfsdk:"vnc_wait"`

	Serial         types.Bool  `tfsdk:"serial"`
	TPMEmulation   types.Bool  `tfsdk:"tpm_emulation"`
	QemuGuestAgent types.Bool  `tfsdk:"qemu_guest_agent"`
	StartAtBoot    types.Bool  `tfsdk:"start_at_boot"`
	StartOrder     types.Int64 `tfsdk:"start_order"`

	StoragePool          types.String `tfsdk:"storage_pool"`
	StorageType          types.String `tfsdk:"storage_type"`
	StorageSize          types.Int64  `tfsdk:"storage_size"`
	StorageEmulationType types.String `tfsdk:"storage_emulation_type"`
	SwitchName           types.String `tfsdk:"switch_name"`
	SwitchEmulationType  types.String `tfsdk:"switch_emulation_type"`
	MacID                types.Int64  `tfsdk:"mac_id"`
	ISO                  types.String `tfsdk:"iso"`

	CloudInit              types.Bool   `tfsdk:"cloud_init"`
	CloudInitData          types.String `tfsdk:"cloud_init_data"`
	CloudInitMetaData      types.String `tfsdk:"cloud_init_metadata"`
	CloudInitNetworkConfig types.String `tfsdk:"cloud_init_network_config"`
}

func (r *vmResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vm"
}

func (r *vmResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}

	resp.Schema = schema.Schema{
		Description: "A bhyve virtual machine managed by Sylve. Covers the VM object itself " +
			"(CPU/RAM/VNC/boot options), its first disk and first NIC (set at creation time -- see " +
			"sylve_vm_storage for additional disks), boot/cloud-init media (iso), and cloud-init itself " +
			"(cloud_init plus its data/metadata/network-config payloads). Additional NICs and PCI " +
			"passthrough are not yet covered -- see the provider's dev notes.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Terraform resource identity -- the VM's resource ID (rid) as a string.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"rid": schema.Int64Attribute{
				Required: true,
				Description: "Sylve's resource ID for this VM, 1-9999, unique across VMs. Sylve does " +
					"NOT auto-assign this despite some docs implying it -- validateCreate rejects rid <= 0 " +
					"outright with invalid_rid (confirmed live, 2026-08-31; see the provider's dev notes). " +
					"The caller must pick one, same as a Proxmox VMID.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "VM name.",
			},
			"description": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Free-text description shown in the Sylve UI.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"ram": schema.Int64Attribute{
				Required:    true,
				Description: "Memory in BYTES (not MiB -- matches the real API; minimum enforced by Sylve is 128 MiB, i.e. 134217728).",
			},
			"cpu_cores": schema.Int64Attribute{
				Required:    true,
				Description: "CPU cores per socket.",
			},
			"cpu_sockets": schema.Int64Attribute{
				Required:    true,
				Description: "Number of CPU sockets.",
			},
			"cpu_threads": schema.Int64Attribute{
				Required:    true,
				Description: "Threads per core.",
			},
			"time_offset": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "\"utc\" (default) or \"localtime\".",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			// vnc_port/vnc_password/vnc_resolution: Sylve's ModifyVNC
			// requires VNCEnabled/VNCPort/VNCResolution/VNCPassword/
			// VNCWait/PCIDevices ALL together, none omittable -- and
			// this resource doesn't track vnc_enabled/vnc_wait/
			// pci_devices at all, so a partial call would risk silently
			// resetting whichever of those the live VM actually has.
			// RequiresReplace here (rather than wiring a half-correct
			// endpoint call) is the safe choice until this resource
			// grows those three fields too. Found alongside the same
			// bug on serial/tpm_emulation/qemu_guest_agent/
			// start_at_boot/start_order (2026-09-01, see above and the
			// dev notes) -- these three had it too (description claimed
			// replacement, no modifier actually did it).
			"vnc_port": schema.Int64Attribute{
				Required:      true,
				Description:   "VNC display port. Required by Sylve's own API (zero is rejected). Changing it recreates the VM.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"vnc_password": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Sensitive:     true,
				Description:   "VNC password. Changing it recreates the VM (see vnc_port).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplace()},
			},
			"vnc_resolution": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "VNC resolution, e.g. \"1024x768\". Changing it recreates the VM (see vnc_port).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplace()},
			},
			"vnc_wait": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Description: "Pause the guest CPU at boot until a VNC client connects. **Defaults to " +
					"false in this resource** -- Sylve's own create service defaults an OMITTED value to " +
					"true, which silently freezes the VM indefinitely (confirmed live, 2026-09-01: a VM " +
					"created before this field existed sat at ~0s CPU time, reporting domain status " +
					"\"Running\" while never actually booting -- see the provider's dev notes). This " +
					"resource always sends an explicit value for exactly that reason; leave it unset for " +
					"normal headless/automated use. Changing it recreates the VM (see vnc_port).",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown(), boolplanmodifier.RequiresReplace()},
			},
			// serial/tpm_emulation/qemu_guest_agent/start_at_boot are
			// deliberately Optional+Computed with UseStateForUnknown and
			// NO static default: a static Default (e.g. booldefault.
			// StaticBool(false)) fires on every plan where config is
			// null, not just on create, which fought the imported/prior
			// state and produced a spurious "true -> false" diff on
			// every subsequent plan of an already-imported VM. Found
			// live testing import against a real, already-running VM
			// (2026-08-31) -- see the provider's dev notes.
			//
			// A DIFFERENT bug lived here until 2026-09-01: these five
			// attributes' own descriptions claimed "changing it recreates
			// the VM", but none actually had a RequiresReplace modifier,
			// and Update() didn't call any endpoint for them either --
			// meaning a config change would plan as an in-place update,
			// Update() would silently do nothing, and the new (wrong)
			// value would still get written into state as if it had
			// succeeded. Found reviewing this schema after a user
			// question about qemu_guest_agent, not by live-testing (the
			// live tests so far only ever set these once, at create).
			// Fixed by actually wiring the real endpoints below rather
			// than only adding RequiresReplace -- see Update().
			"serial": schema.BoolAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Enable the serial console. Updated in place via ModifySerialConsole.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"tpm_emulation": schema.BoolAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Emulate a TPM device. Updated in place via ModifyTPM.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"qemu_guest_agent": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Description: "Enable the QEMU guest agent channel (the enable flag only -- reading live " +
					"guest info such as its IP addresses via GET /vm/qga/:rid is not covered by this " +
					"provider at all; there are no data sources yet). Updated in place via " +
					"ModifyQemuGuestAgent.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"start_at_boot": schema.BoolAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Start this VM automatically when Sylve/the host boots. Updated in place via ModifyBootOrder (shares one endpoint with start_order -- both travel in the same request).",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"start_order": schema.Int64Attribute{
				Optional:      true,
				Computed:      true,
				Description:   "Boot order among auto-started guests. Updated in place, see start_at_boot.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"storage_pool": schema.StringAttribute{
				Optional:      true,
				Description:   "ZFS pool for the VM's first disk, e.g. \"tank\". Write-only: not read back (see resource description); changing it recreates the VM.",
				PlanModifiers: replace,
			},
			"storage_type": schema.StringAttribute{
				Optional:      true,
				Description:   "First disk type: \"zvol\", \"raw\", \"image\", or \"filesystem\". Write-only, see storage_pool.",
				PlanModifiers: replace,
			},
			"storage_size": schema.Int64Attribute{
				Optional:      true,
				Description:   "First disk size in bytes. Write-only, see storage_pool.",
				PlanModifiers: []planmodifier.Int64{},
			},
			"storage_emulation_type": schema.StringAttribute{
				Optional:      true,
				Description:   "First disk emulation: \"virtio-blk\", \"virtio-9p\", \"ahci-hd\", \"ahci-cd\", or \"nvme\". Write-only, see storage_pool.",
				PlanModifiers: replace,
			},
			"switch_name": schema.StringAttribute{
				Optional:      true,
				Description:   "Name of an existing Sylve network switch to attach the first NIC to. Write-only, see storage_pool. Requires switch_emulation_type to be set too (found live, 2026-09-01: omitting it fails with no_switch_emulation_type_selected -- this field was missed entirely when this resource was first fixed, since no earlier test actually attached networking).",
				PlanModifiers: replace,
			},
			"switch_emulation_type": schema.StringAttribute{
				Optional:      true,
				Description:   "NIC emulation type, e.g. \"virtio\" (confirmed against a live VM's own network entry -- not the same value space as storage_emulation_type's \"virtio-blk\"). Required whenever switch_name is set to a real switch (not \"none\"). Write-only, see storage_pool.",
				PlanModifiers: replace,
			},
			"mac_id": schema.Int64Attribute{
				Optional:      true,
				Description:   "ID of an existing Sylve MAC network-object to use for the first NIC, instead of letting Sylve generate one. Write-only, see storage_pool.",
				PlanModifiers: []planmodifier.Int64{},
			},
			"iso": schema.StringAttribute{
				Optional: true,
				Description: "UUID of a sylve_download to boot from / use as a cloud-init source " +
					"(typically one with utype \"cloud-init\" when cloud_init is enabled -- Sylve " +
					"validates that combination and rejects a non-cloud-init-capable image). Write-only, " +
					"see storage_pool.",
				PlanModifiers: replace,
			},
			"cloud_init": schema.BoolAttribute{
				Optional: true,
				Description: "Enable cloud-init for this VM. Genuinely create-time-only: Sylve has no " +
					"persisted \"is cloud-init enabled\" column at all (confirmed against its VM model) " +
					"-- this flag only steers create-time validation of iso (does it need to resolve to " +
					"a cloud-init-capable download or not). Nothing to read back, so changing it forces " +
					"replacement rather than silently doing nothing.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.RequiresReplace()},
			},
			"cloud_init_data": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "cloud-init user-data. Updatable in place via a dedicated endpoint (unlike iso itself).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"cloud_init_metadata": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "cloud-init meta-data. Updatable in place, see cloud_init_data.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"cloud_init_network_config": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "cloud-init network-config. Updatable in place, see cloud_init_data.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *vmResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*sylveclient.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected resource configure type",
			fmt.Sprintf("Expected *sylveclient.Client, got: %T. Report this issue to the provider maintainers.", req.ProviderData),
		)
		return
	}
	r.client = client
}

func (r *vmResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan vmResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	timeOffset := plan.TimeOffset.ValueString()
	if timeOffset == "" {
		timeOffset = "utc"
	}

	// Unlike switch_name (empty is treated as "no NIC"), Sylve's
	// validateCreate treats an empty storageType as "attach a disk from
	// pool ''" and fails with pool_not_found -- it wants the literal
	// string "none" to mean "no disk". Confirmed live, 2026-08-31 (see
	// the provider's dev notes).
	storageType := plan.StorageType.ValueString()
	if storageType == "" {
		storageType = "none"
	}
	// Sylve also requires a non-empty storageEmulationType whenever
	// storageType is non-empty -- including "none" itself, where the
	// value is otherwise unused. Any of the enum's values satisfies the
	// check; "virtio-blk" is arbitrary.
	storageEmulationType := plan.StorageEmulationType.ValueString()
	if storageEmulationType == "" {
		storageEmulationType = "virtio-blk"
	}
	// Also required by validateCreate despite no `binding:"required"` tag
	// on the Go struct field -- a service-level check, not a gin-binding
	// one. "1024x768" matches Sylve's own UI default.
	vncResolution := plan.VNCResolution.ValueString()
	if vncResolution == "" {
		vncResolution = "1024x768"
	}

	created, err := r.client.CreateVM(ctx, sylveclient.VM{
		Name:                   plan.Name.ValueString(),
		RID:                    int(plan.RID.ValueInt64()),
		Description:            plan.Description.ValueString(),
		RAM:                    plan.RAM.ValueInt64(),
		CPUCores:               int(plan.CPUCores.ValueInt64()),
		CPUSockets:             int(plan.CPUSockets.ValueInt64()),
		CPUThreads:             int(plan.CPUThreads.ValueInt64()),
		TimeOffset:             timeOffset,
		VNCPort:                int(plan.VNCPort.ValueInt64()),
		VNCPassword:            plan.VNCPassword.ValueString(),
		VNCResolution:          vncResolution,
		VNCWait:                plan.VNCWait.ValueBool(),
		Serial:                 plan.Serial.ValueBool(),
		TPMEmulation:           plan.TPMEmulation.ValueBool(),
		QemuGuestAgent:         plan.QemuGuestAgent.ValueBool(),
		StartAtBoot:            plan.StartAtBoot.ValueBool(),
		StartOrder:             int(plan.StartOrder.ValueInt64()),
		StoragePool:            plan.StoragePool.ValueString(),
		StorageType:            storageType,
		StorageSize:            uint64(plan.StorageSize.ValueInt64()),
		StorageEmulationType:   storageEmulationType,
		SwitchName:             plan.SwitchName.ValueString(),
		SwitchEmulationType:    plan.SwitchEmulationType.ValueString(),
		MacID:                  int(plan.MacID.ValueInt64()),
		ISO:                    plan.ISO.ValueString(),
		CloudInit:              plan.CloudInit.ValueBool(),
		CloudInitData:          plan.CloudInitData.ValueString(),
		CloudInitMetaData:      plan.CloudInitMetaData.ValueString(),
		CloudInitNetworkConfig: plan.CloudInitNetworkConfig.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Error creating Sylve VM", err.Error())
		return
	}

	plan.ID = types.StringValue(strconv.Itoa(created.RID))
	plan.RID = types.Int64Value(int64(created.RID))
	plan.Description = types.StringValue(created.Description)
	plan.TimeOffset = types.StringValue(created.TimeOffset)
	plan.VNCPassword = types.StringValue(created.VNCPassword)
	plan.VNCResolution = types.StringValue(created.VNCResolution)
	plan.VNCWait = types.BoolValue(created.VNCWait)
	plan.Serial = types.BoolValue(created.Serial)
	plan.TPMEmulation = types.BoolValue(created.TPMEmulation)
	plan.QemuGuestAgent = types.BoolValue(created.QemuGuestAgent)
	plan.StartAtBoot = types.BoolValue(created.StartAtBoot)
	plan.StartOrder = types.Int64Value(int64(created.StartOrder))
	plan.CloudInitData = types.StringValue(created.CloudInitData)
	plan.CloudInitMetaData = types.StringValue(created.CloudInitMetaData)
	plan.CloudInitNetworkConfig = types.StringValue(created.CloudInitNetworkConfig)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *vmResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state vmResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	vm, err := r.client.GetVM(ctx, int(state.RID.ValueInt64()))
	if err != nil {
		if sylveclient.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Sylve VM", err.Error())
		return
	}

	state.Name = types.StringValue(vm.Name)
	state.Description = types.StringValue(vm.Description)
	state.RAM = types.Int64Value(vm.RAM)
	state.CPUCores = types.Int64Value(int64(vm.CPUCores))
	state.CPUSockets = types.Int64Value(int64(vm.CPUSockets))
	state.CPUThreads = types.Int64Value(int64(vm.CPUThreads))
	state.TimeOffset = types.StringValue(vm.TimeOffset)
	state.VNCPort = types.Int64Value(int64(vm.VNCPort))
	state.VNCPassword = types.StringValue(vm.VNCPassword)
	state.VNCResolution = types.StringValue(vm.VNCResolution)
	state.VNCWait = types.BoolValue(vm.VNCWait)
	state.Serial = types.BoolValue(vm.Serial)
	state.TPMEmulation = types.BoolValue(vm.TPMEmulation)
	state.QemuGuestAgent = types.BoolValue(vm.QemuGuestAgent)
	state.StartAtBoot = types.BoolValue(vm.StartAtBoot)
	state.StartOrder = types.Int64Value(int64(vm.StartOrder))
	state.CloudInitData = types.StringValue(vm.CloudInitData)
	state.CloudInitMetaData = types.StringValue(vm.CloudInitMetaData)
	state.CloudInitNetworkConfig = types.StringValue(vm.CloudInitNetworkConfig)
	// storage_pool/storage_type/storage_size/storage_emulation_type/
	// switch_name/mac_id/iso/cloud_init are deliberately left untouched:
	// Sylve's GET response doesn't echo them back flat (or, for
	// cloud_init, doesn't persist it at all -- see that attribute's own
	// schema description), so whatever's already in state -- from a
	// prior apply or from Import -- is left as the answer.

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *vmResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state vmResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	rid := int(state.RID.ValueInt64())

	if plan.Name.ValueString() != state.Name.ValueString() {
		if err := r.client.SetVMName(ctx, rid, plan.Name.ValueString()); err != nil {
			resp.Diagnostics.AddError("Error updating Sylve VM name", err.Error())
			return
		}
	}
	if plan.Description.ValueString() != state.Description.ValueString() {
		if err := r.client.SetVMDescription(ctx, rid, plan.Description.ValueString()); err != nil {
			resp.Diagnostics.AddError("Error updating Sylve VM description", err.Error())
			return
		}
	}
	if plan.CPUCores.ValueInt64() != state.CPUCores.ValueInt64() ||
		plan.CPUSockets.ValueInt64() != state.CPUSockets.ValueInt64() ||
		plan.CPUThreads.ValueInt64() != state.CPUThreads.ValueInt64() {
		if err := r.client.SetVMCPU(ctx, rid,
			int(plan.CPUCores.ValueInt64()), int(plan.CPUSockets.ValueInt64()), int(plan.CPUThreads.ValueInt64()),
		); err != nil {
			resp.Diagnostics.AddError("Error updating Sylve VM CPU", err.Error())
			return
		}
	}
	if plan.RAM.ValueInt64() != state.RAM.ValueInt64() {
		if err := r.client.SetVMRAM(ctx, rid, plan.RAM.ValueInt64()); err != nil {
			resp.Diagnostics.AddError("Error updating Sylve VM RAM", err.Error())
			return
		}
	}
	if plan.TimeOffset.ValueString() != state.TimeOffset.ValueString() {
		if err := r.client.SetVMTimeOffset(ctx, rid, plan.TimeOffset.ValueString()); err != nil {
			resp.Diagnostics.AddError("Error updating Sylve VM clock", err.Error())
			return
		}
	}
	if plan.Serial.ValueBool() != state.Serial.ValueBool() {
		if err := r.client.SetVMSerialConsole(ctx, rid, plan.Serial.ValueBool()); err != nil {
			resp.Diagnostics.AddError("Error updating Sylve VM serial console", err.Error())
			return
		}
	}
	if plan.TPMEmulation.ValueBool() != state.TPMEmulation.ValueBool() {
		if err := r.client.SetVMTPM(ctx, rid, plan.TPMEmulation.ValueBool()); err != nil {
			resp.Diagnostics.AddError("Error updating Sylve VM TPM emulation", err.Error())
			return
		}
	}
	if plan.QemuGuestAgent.ValueBool() != state.QemuGuestAgent.ValueBool() {
		if err := r.client.SetVMQemuGuestAgent(ctx, rid, plan.QemuGuestAgent.ValueBool()); err != nil {
			resp.Diagnostics.AddError("Error updating Sylve VM QEMU guest agent", err.Error())
			return
		}
	}
	if plan.StartAtBoot.ValueBool() != state.StartAtBoot.ValueBool() ||
		plan.StartOrder.ValueInt64() != state.StartOrder.ValueInt64() {
		if err := r.client.SetVMBootOrder(ctx, rid, plan.StartAtBoot.ValueBool(), int(plan.StartOrder.ValueInt64())); err != nil {
			resp.Diagnostics.AddError("Error updating Sylve VM boot order", err.Error())
			return
		}
	}
	if plan.CloudInitData.ValueString() != state.CloudInitData.ValueString() ||
		plan.CloudInitMetaData.ValueString() != state.CloudInitMetaData.ValueString() ||
		plan.CloudInitNetworkConfig.ValueString() != state.CloudInitNetworkConfig.ValueString() {
		if err := r.client.SetVMCloudInit(ctx, rid,
			plan.CloudInitData.ValueString(), plan.CloudInitMetaData.ValueString(), plan.CloudInitNetworkConfig.ValueString(),
		); err != nil {
			resp.Diagnostics.AddError("Error updating Sylve VM cloud-init data", err.Error())
			return
		}
	}

	plan.ID = state.ID
	plan.RID = state.RID
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *vmResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state vmResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.DeleteVM(ctx, int(state.RID.ValueInt64()))
	if err != nil && !sylveclient.IsNotFound(err) {
		resp.Diagnostics.AddError("Error deleting Sylve VM", err.Error())
	}
}

// ImportState accepts a bare RID (e.g. `terraform import sylve_vm.foo
// 252`) and seeds both id and rid from it; Read then fills in everything
// else. storage_pool/storage_type/storage_size/storage_emulation_type/
// switch_name/mac_id come back empty on an imported resource (see Read's
// own comment) -- a first `terraform plan` after import will show those
// as changes from "" to whatever the config declares, same as any other
// write-only/unreadable attribute. Reconcile the config to match reality
// (or drop it from config and accept drift) before applying.
func (r *vmResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	rid, err := strconv.ParseInt(req.ID, 10, 64)
	if err != nil {
		resp.Diagnostics.AddError(
			"Invalid import ID",
			fmt.Sprintf("Expected a numeric Sylve VM RID (e.g. \"101\"), got %q: %s", req.ID, err),
		)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("rid"), rid)...)
}
