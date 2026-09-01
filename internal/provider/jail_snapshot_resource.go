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
	_ resource.Resource                = &jailSnapshotResource{}
	_ resource.ResourceWithConfigure   = &jailSnapshotResource{}
	_ resource.ResourceWithImportState = &jailSnapshotResource{}
)

// NewJailSnapshotResource is the constructor registered in provider.Resources.
func NewJailSnapshotResource() resource.Resource {
	return &jailSnapshotResource{}
}

type jailSnapshotResource struct {
	client *sylveclient.Client
}

// jailSnapshotResourceModel is structurally parallel to
// vmSnapshotResourceModel -- see sylve_vm_snapshot's own doc comment,
// same reasoning throughout (immutable name/description, no shutoff
// requirement, root_dataset singular here vs. root_datasets plural on
// the VM side since a jail has exactly one root dataset).
type jailSnapshotResourceModel struct {
	ID           types.String `tfsdk:"id"`
	CTID         types.Int64  `tfsdk:"ctid"`
	Name         types.String `tfsdk:"name"`
	Description  types.String `tfsdk:"description"`
	SnapshotName types.String `tfsdk:"snapshot_name"`
	RootDataset  types.String `tfsdk:"root_dataset"`
}

func (r *jailSnapshotResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_jail_snapshot"
}

func (r *jailSnapshotResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}

	resp.Schema = schema.Schema{
		Description: "A point-in-time ZFS snapshot of a sylve_jail's root dataset. Structurally " +
			"parallel to sylve_vm_snapshot -- see that resource's own description for the shared " +
			"reasoning (crash-consistent regardless of running state, immutable name/description, no " +
			"rename endpoint). See sylve_jail_snapshot_rollback to actually restore one.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Terraform resource identity -- the snapshot's own numeric ID.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"ctid": schema.Int64Attribute{
				Required:      true,
				Description:   "CTID of the sylve_jail to snapshot. Immutable.",
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
			"root_dataset": schema.StringAttribute{
				Computed:      true,
				Description:   "ZFS dataset this snapshot covers.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *jailSnapshotResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func setJailSnapshotComputed(m *jailSnapshotResourceModel, s *sylveclient.JailSnapshot) {
	m.ID = types.StringValue(strconv.Itoa(s.ID))
	m.SnapshotName = types.StringValue(s.SnapshotName)
	m.RootDataset = types.StringValue(s.RootDataset)
}

func (r *jailSnapshotResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan jailSnapshotResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	created, err := r.client.CreateJailSnapshot(ctx, int(plan.CTID.ValueInt64()), plan.Name.ValueString(), plan.Description.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error creating Sylve jail snapshot", err.Error())
		return
	}

	setJailSnapshotComputed(&plan, created)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *jailSnapshotResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state jailSnapshotResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, err := strconv.Atoi(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid state", fmt.Sprintf("id %q is not numeric: %s", state.ID.ValueString(), err))
		return
	}

	snap, err := r.client.GetJailSnapshot(ctx, int(state.CTID.ValueInt64()), id)
	if err != nil {
		if sylveclient.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Sylve jail snapshot", err.Error())
		return
	}

	state.Name = types.StringValue(snap.Name)
	state.Description = types.StringValue(snap.Description)
	setJailSnapshotComputed(&state, snap)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update is never reached in practice: every attribute forces
// replacement -- but the interface requires an implementation.
func (r *jailSnapshotResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan jailSnapshotResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *jailSnapshotResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state jailSnapshotResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, err := strconv.Atoi(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid state", fmt.Sprintf("id %q is not numeric: %s", state.ID.ValueString(), err))
		return
	}

	err = r.client.DeleteJailSnapshot(ctx, int(state.CTID.ValueInt64()), id)
	if err != nil && !sylveclient.IsNotFound(err) {
		resp.Diagnostics.AddError("Error deleting Sylve jail snapshot", err.Error())
	}
}

// ImportState accepts "<ctid>/<snapshotId>".
func (r *jailSnapshotResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.SplitN(req.ID, "/", 2)
	if len(parts) != 2 {
		resp.Diagnostics.AddError(
			"Invalid import ID",
			fmt.Sprintf("Expected \"<ctid>/<snapshotId>\", e.g. \"100/1\", got %q", req.ID),
		)
		return
	}
	ctid, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("ctid %q is not numeric: %s", parts[0], err))
		return
	}
	if _, err := strconv.Atoi(parts[1]); err != nil {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("snapshotId %q is not numeric: %s", parts[1], err))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("ctid"), ctid)...)
}
