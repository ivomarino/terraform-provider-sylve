package provider

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ivomarino/terraform-provider-sylve/internal/sylveclient"
)

var (
	_ resource.Resource                = &vmSnapshotResource{}
	_ resource.ResourceWithConfigure   = &vmSnapshotResource{}
	_ resource.ResourceWithImportState = &vmSnapshotResource{}
)

// NewVMSnapshotResource is the constructor registered in provider.Resources.
func NewVMSnapshotResource() resource.Resource {
	return &vmSnapshotResource{}
}

type vmSnapshotResource struct {
	client *sylveclient.Client
}

type vmSnapshotResourceModel struct {
	ID           types.String `tfsdk:"id"` // snapshot's own numeric id
	RID          types.Int64  `tfsdk:"rid"`
	Name         types.String `tfsdk:"name"`
	Description  types.String `tfsdk:"description"`
	SnapshotName types.String `tfsdk:"snapshot_name"`
	RootDatasets types.List   `tfsdk:"root_datasets"`
}

func (r *vmSnapshotResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vm_snapshot"
}

func (r *vmSnapshotResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}

	resp.Schema = schema.Schema{
		Description: "A point-in-time ZFS snapshot of a sylve_vm's root dataset(s). Crash-consistent " +
			"and can be taken whether the VM is running or stopped -- no shutoff requirement, unlike " +
			"sylve_vm_storage. See sylve_vm_snapshot_rollback to actually restore one. There is no " +
			"rename endpoint, so name/description are immutable; changing either recreates the snapshot " +
			"(i.e. deletes the old one and takes a fresh one -- it does NOT edit history in place).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Terraform resource identity -- the snapshot's own numeric ID.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"rid": schema.Int64Attribute{
				Required:      true,
				Description:   "RID of the sylve_vm to snapshot. Immutable.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"name": schema.StringAttribute{
				Required:      true,
				Description:   "Snapshot name. Immutable (no rename endpoint).",
				PlanModifiers: replace,
			},
			"description": schema.StringAttribute{
				Optional:      true,
				Description:   "Free-text description. Immutable, same reason as name.",
				PlanModifiers: replace,
			},
			"snapshot_name": schema.StringAttribute{
				Computed:      true,
				Description:   "Sylve's own internal ZFS snapshot name (distinct from the display name above).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"root_datasets": schema.ListAttribute{
				ElementType: types.StringType,
				Computed:    true,
				Description: "ZFS dataset(s) this snapshot covers.",
			},
		},
	}
}

func (r *vmSnapshotResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func setSnapshotComputed(ctx context.Context, m *vmSnapshotResourceModel, s *sylveclient.VMSnapshot) {
	m.ID = types.StringValue(strconv.Itoa(s.ID))
	m.SnapshotName = types.StringValue(s.SnapshotName)
	rootDatasets, _ := types.ListValueFrom(ctx, types.StringType, s.RootDatasets)
	m.RootDatasets = rootDatasets
}

func (r *vmSnapshotResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan vmSnapshotResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	created, err := r.client.CreateVMSnapshot(ctx, int(plan.RID.ValueInt64()), plan.Name.ValueString(), plan.Description.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error creating Sylve VM snapshot", err.Error())
		return
	}

	setSnapshotComputed(ctx, &plan, created)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *vmSnapshotResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state vmSnapshotResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, err := strconv.Atoi(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid state", fmt.Sprintf("id %q is not numeric: %s", state.ID.ValueString(), err))
		return
	}

	snap, err := r.client.GetVMSnapshot(ctx, int(state.RID.ValueInt64()), id)
	if err != nil {
		if sylveclient.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Sylve VM snapshot", err.Error())
		return
	}

	state.Name = types.StringValue(snap.Name)
	state.Description = types.StringValue(snap.Description)
	setSnapshotComputed(ctx, &state, snap)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update is never reached in practice: every attribute forces
// replacement -- but the interface requires an implementation.
func (r *vmSnapshotResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan vmSnapshotResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *vmSnapshotResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state vmSnapshotResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, err := strconv.Atoi(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid state", fmt.Sprintf("id %q is not numeric: %s", state.ID.ValueString(), err))
		return
	}

	err = r.client.DeleteVMSnapshot(ctx, int(state.RID.ValueInt64()), id)
	if err != nil && !sylveclient.IsNotFound(err) {
		resp.Diagnostics.AddError("Error deleting Sylve VM snapshot", err.Error())
	}
}

// ImportState accepts "<rid>/<snapshotId>", e.g. "101/1" -- same
// two-part shape as sylve_vm_storage, for the same reason: there's no
// per-snapshot GET, only the per-VM list, so the RID is needed to look
// it up at all.
func (r *vmSnapshotResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.SplitN(req.ID, "/", 2)
	if len(parts) != 2 {
		resp.Diagnostics.AddError(
			"Invalid import ID",
			fmt.Sprintf("Expected \"<rid>/<snapshotId>\", e.g. \"101/1\", got %q", req.ID),
		)
		return
	}
	rid, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("rid %q is not numeric: %s", parts[0], err))
		return
	}
	if _, err := strconv.Atoi(parts[1]); err != nil {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("snapshotId %q is not numeric: %s", parts[1], err))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("rid"), rid)...)
}
