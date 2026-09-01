package provider

import (
	"context"
	"fmt"
	"strconv"
	"strings"

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
	_ resource.Resource                = &vmStorageResource{}
	_ resource.ResourceWithConfigure   = &vmStorageResource{}
	_ resource.ResourceWithImportState = &vmStorageResource{}
)

// NewVMStorageResource is the constructor registered in provider.Resources.
func NewVMStorageResource() resource.Resource {
	return &vmStorageResource{}
}

type vmStorageResource struct {
	client *sylveclient.Client
}

type vmStorageResourceModel struct {
	ID   types.String `tfsdk:"id"` // storage's own ID (NOT the VM's rid)
	RID  types.Int64  `tfsdk:"rid"`
	Name types.String `tfsdk:"name"`

	AttachType  types.String `tfsdk:"attach_type"`
	StorageType types.String `tfsdk:"storage_type"`
	Emulation   types.String `tfsdk:"emulation"`

	Pool         types.String `tfsdk:"pool"`
	Size         types.Int64  `tfsdk:"size"`
	Dataset      types.String `tfsdk:"dataset"`
	DownloadUUID types.String `tfsdk:"download_uuid"`
	RawPath      types.String `tfsdk:"raw_path"`

	FilesystemTarget types.String `tfsdk:"filesystem_target"`
	ReadOnly         types.Bool   `tfsdk:"read_only"`
	BootOrder        types.Int64  `tfsdk:"boot_order"`
	Enable           types.Bool   `tfsdk:"enable"`
}

func (r *vmStorageResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vm_storage"
}

func (r *vmStorageResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}

	resp.Schema = schema.Schema{
		Description: "A disk or CD-ROM attached to a sylve_vm beyond its create-time first disk -- the " +
			"real mechanism for giving a VM a boot disk sourced from a downloaded cloud image (see " +
			"sylve_zfs_volume's flash_from_download_uuid to get the image onto a volume first, then " +
			"attach_type = \"import\", storage_type = \"zvol\", dataset = that volume's id here). The " +
			"target VM MUST be stopped -- Sylve rejects attach/update/detach outright with " +
			"domain_state_not_shutoff otherwise; this resource does not stop/start the VM for you.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Terraform resource identity -- this storage entry's own numeric ID (distinct from the VM's rid).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"rid": schema.Int64Attribute{
				Required:      true,
				Description:   "RID of the sylve_vm to attach this storage to. Immutable -- moving storage between VMs isn't supported by this resource.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Storage entry name, shown in the Sylve UI.",
			},
			"attach_type": schema.StringAttribute{
				Required: true,
				Description: "\"new\" (create a fresh disk) or \"import\" (adopt an existing zvol via " +
					"dataset, or attach a download directly via download_uuid). Immutable.",
				PlanModifiers: replace,
			},
			"storage_type": schema.StringAttribute{
				Required:      true,
				Description:   "\"raw\", \"zvol\", \"image\", or \"filesystem\". Immutable.",
				PlanModifiers: replace,
			},
			"emulation": schema.StringAttribute{
				Required: true,
				Description: "\"virtio-blk\", \"virtio-9p\", \"ahci-hd\", \"ahci-cd\", or \"nvme\". Note: " +
					"unlike sylve_vm's create-time iso attribute (which always hardcodes \"ahci-cd\"), " +
					"attaching a storage_type = \"image\" download here with emulation = \"virtio-blk\" " +
					"makes it usable as a real boot disk, not just CD-ROM media. Updatable in place.",
			},
			"pool": schema.StringAttribute{
				Optional: true,
				Description: "ZFS pool, required by Sylve for storage_type raw/zvol (either attach_type). " +
					"Immutable.",
				PlanModifiers: replace,
			},
			"size": schema.Int64Attribute{
				Optional:      true,
				Computed:      true,
				Description:   "Size in bytes, for attach_type = \"new\". Updatable in place (grows the disk; shrinking is not validated client-side, same as elsewhere in this provider).",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"dataset": schema.StringAttribute{
				Optional: true,
				Description: "Source zvol's dataset GUID, for attach_type = \"import\" + storage_type = " +
					"\"zvol\" -- typically a sylve_zfs_volume already flashed from a downloaded cloud " +
					"image. Sylve renames/clones this dataset into the VM's own dataset namespace; the " +
					"source sylve_zfs_volume resource should generally be considered consumed afterward. " +
					"Immutable.",
				PlanModifiers: replace,
			},
			"download_uuid": schema.StringAttribute{
				Optional: true,
				Description: "sylve_download UUID, for attach_type = \"import\" + storage_type = " +
					"\"image\" -- attaches the download directly by reference (not copied), with " +
					"emulation as the caller's choice (see that attribute's own description). Immutable.",
				PlanModifiers: replace,
			},
			"raw_path": schema.StringAttribute{
				Optional:      true,
				Description:   "Absolute filesystem path to an existing raw disk image, for attach_type = \"import\" + storage_type = \"raw\". Immutable.",
				PlanModifiers: replace,
			},
			"filesystem_target": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Target name, for storage_type = \"filesystem\". Updatable in place.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"read_only": schema.BoolAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Attach as read-only. Updatable in place.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"boot_order": schema.Int64Attribute{
				Optional:      true,
				Computed:      true,
				Description:   "Boot order among this VM's storage entries. Defaults to the next free index if unset at create. Updatable in place.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"enable": schema.BoolAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Whether this storage entry is active. Updatable in place.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *vmStorageResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *vmStorageResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan vmStorageResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	rid := int(plan.RID.ValueInt64())
	name := plan.Name.ValueString()

	params := sylveclient.AttachStorageParams{
		RID:              rid,
		Name:             name,
		AttachType:       plan.AttachType.ValueString(),
		StorageType:      plan.StorageType.ValueString(),
		Emulation:        plan.Emulation.ValueString(),
		Pool:             plan.Pool.ValueString(),
		Dataset:          plan.Dataset.ValueString(),
		DownloadUUID:     plan.DownloadUUID.ValueString(),
		RawPath:          plan.RawPath.ValueString(),
		FilesystemTarget: plan.FilesystemTarget.ValueString(),
		ReadOnly:         plan.ReadOnly.ValueBool(),
	}
	if !plan.Size.IsNull() && !plan.Size.IsUnknown() {
		params.Size = plan.Size.ValueInt64()
	}
	if !plan.BootOrder.IsNull() && !plan.BootOrder.IsUnknown() {
		params.BootOrder = int(plan.BootOrder.ValueInt64())
		params.HasBootOrder = true
	}

	if err := r.client.AttachStorage(ctx, params); err != nil {
		resp.Diagnostics.AddError("Error attaching Sylve VM storage", err.Error())
		return
	}

	created, err := r.client.FindVMStorageByName(ctx, rid, name)
	if err != nil {
		resp.Diagnostics.AddError("Sylve VM storage attached but could not be looked up afterwards", err.Error())
		return
	}

	plan.ID = types.StringValue(strconv.Itoa(created.ID))
	plan.Emulation = types.StringValue(created.Emulation)
	plan.Size = types.Int64Value(created.Size)
	plan.FilesystemTarget = types.StringValue(created.FilesystemTarget)
	plan.ReadOnly = types.BoolValue(created.ReadOnly)
	plan.BootOrder = types.Int64Value(int64(created.BootOrder))
	plan.Enable = types.BoolValue(created.Enable)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *vmStorageResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state vmStorageResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, err := strconv.Atoi(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid state", fmt.Sprintf("id %q is not numeric: %s", state.ID.ValueString(), err))
		return
	}

	storage, err := r.client.GetVMStorage(ctx, int(state.RID.ValueInt64()), id)
	if err != nil {
		if sylveclient.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Sylve VM storage", err.Error())
		return
	}

	state.Name = types.StringValue(storage.Name)
	state.Emulation = types.StringValue(storage.Emulation)
	state.Size = types.Int64Value(storage.Size)
	state.FilesystemTarget = types.StringValue(storage.FilesystemTarget)
	state.ReadOnly = types.BoolValue(storage.ReadOnly)
	state.BootOrder = types.Int64Value(int64(storage.BootOrder))
	state.Enable = types.BoolValue(storage.Enable)
	// attach_type/pool/dataset/download_uuid/raw_path are deliberately
	// left untouched -- create-time-only concepts not echoed back by
	// GET /vm/:id (pool and download_uuid are the exceptions on the
	// wire, see VMStorage's own fields, but are left alone here anyway
	// for consistency with the rest of this attribute set, all of which
	// share the same "write-only, changing forces replacement" schema
	// treatment).

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *vmStorageResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state vmStorageResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, err := strconv.Atoi(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid state", fmt.Sprintf("id %q is not numeric: %s", state.ID.ValueString(), err))
		return
	}

	params := sylveclient.UpdateStorageParams{
		ID:               id,
		Name:             plan.Name.ValueString(),
		Emulation:        plan.Emulation.ValueString(),
		Enable:           plan.Enable.ValueBool(),
		FilesystemTarget: plan.FilesystemTarget.ValueString(),
		ReadOnly:         plan.ReadOnly.ValueBool(),
	}
	if !plan.Size.IsNull() && !plan.Size.IsUnknown() {
		params.Size = plan.Size.ValueInt64()
		params.HasSize = true
	}
	if !plan.BootOrder.IsNull() && !plan.BootOrder.IsUnknown() {
		params.BootOrder = int(plan.BootOrder.ValueInt64())
		params.HasBootOrder = true
	}

	if err := r.client.UpdateStorage(ctx, params); err != nil {
		resp.Diagnostics.AddError("Error updating Sylve VM storage", err.Error())
		return
	}

	plan.ID = state.ID
	plan.RID = state.RID
	plan.AttachType = state.AttachType
	plan.StorageType = state.StorageType
	plan.Pool = state.Pool
	plan.Dataset = state.Dataset
	plan.DownloadUUID = state.DownloadUUID
	plan.RawPath = state.RawPath
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *vmStorageResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state vmStorageResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, err := strconv.Atoi(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid state", fmt.Sprintf("id %q is not numeric: %s", state.ID.ValueString(), err))
		return
	}

	err = r.client.DetachStorage(ctx, int(state.RID.ValueInt64()), id)
	if err != nil && !sylveclient.IsNotFound(err) {
		resp.Diagnostics.AddError("Error detaching Sylve VM storage", err.Error())
	}
}

// ImportState accepts "<rid>/<storageId>", e.g. "101/3" -- a storage
// entry's own ID is only unique per-VM in practice (it's the DB row's
// own auto-increment primary key, shared across all VMs' storage rows,
// so it IS technically globally unique -- but GetVMStorage still needs
// the VM's rid to look it up via GET /vm/:id, there's no per-storage GET
// endpoint), so the import ID has to carry both.
func (r *vmStorageResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.SplitN(req.ID, "/", 2)
	if len(parts) != 2 {
		resp.Diagnostics.AddError(
			"Invalid import ID",
			fmt.Sprintf("Expected \"<rid>/<storageId>\", e.g. \"101/3\", got %q", req.ID),
		)
		return
	}
	rid, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("rid %q is not numeric: %s", parts[0], err))
		return
	}
	if _, err := strconv.Atoi(parts[1]); err != nil {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("storageId %q is not numeric: %s", parts[1], err))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("rid"), rid)...)
}
