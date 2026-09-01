package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ivomarino/terraform-provider-sylve/internal/sylveclient"
)

var (
	_ resource.Resource              = &jailSnapshotRollbackResource{}
	_ resource.ResourceWithConfigure = &jailSnapshotRollbackResource{}
)

// NewJailSnapshotRollbackResource is the constructor registered in provider.Resources.
func NewJailSnapshotRollbackResource() resource.Resource {
	return &jailSnapshotRollbackResource{}
}

type jailSnapshotRollbackResource struct {
	client *sylveclient.Client
}

// jailSnapshotRollbackResourceModel: structurally and philosophically
// identical to vmSnapshotRollbackResourceModel -- see that resource's
// own doc comment for the full reasoning (one-shot action, `trigger` +
// RequiresReplace to re-fire, deliberately no-op Delete).
type jailSnapshotRollbackResourceModel struct {
	ID         types.String `tfsdk:"id"`
	CTID       types.Int64  `tfsdk:"ctid"`
	SnapshotID types.Int64  `tfsdk:"snapshot_id"`
	Trigger    types.String `tfsdk:"trigger"`
}

func (r *jailSnapshotRollbackResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_jail_snapshot_rollback"
}

func (r *jailSnapshotRollbackResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Rolls a sylve_jail back to a prior sylve_jail_snapshot. One-shot ACTION, not " +
			"persistent state -- see sylve_vm_snapshot_rollback's own description for the full reasoning " +
			"(shared: `trigger` re-fires on change, `Delete` is a deliberate no-op, Sylve hardcodes " +
			"destroying newer state as part of the rollback with no partial option).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Terraform resource identity. Not a real external ID -- just \"<ctid>-<snapshot_id>\" for display.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"ctid": schema.Int64Attribute{
				Required:    true,
				Description: "CTID of the sylve_jail to roll back.",
			},
			"snapshot_id": schema.Int64Attribute{
				Required:    true,
				Description: "ID of the sylve_jail_snapshot to roll back to.",
			},
			"trigger": schema.StringAttribute{
				Required:      true,
				Description:   "Arbitrary string. Changing it re-fires the rollback -- see the resource's own description.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
		},
	}
}

func (r *jailSnapshotRollbackResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *jailSnapshotRollbackResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan jailSnapshotRollbackResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	ctid := int(plan.CTID.ValueInt64())
	snapshotID := int(plan.SnapshotID.ValueInt64())
	if err := r.client.RollbackJailSnapshot(ctx, ctid, snapshotID); err != nil {
		resp.Diagnostics.AddError("Error rolling back Sylve jail snapshot", err.Error())
		return
	}

	plan.ID = types.StringValue(fmt.Sprintf("%d-%d", ctid, snapshotID))
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read is deliberately a no-op past the initial Get/Set -- see the
// model's own doc comment.
func (r *jailSnapshotRollbackResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state jailSnapshotRollbackResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update is unreachable: `trigger` is the only attribute that isn't
// Computed, and it forces replacement.
func (r *jailSnapshotRollbackResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan jailSnapshotRollbackResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete is deliberately a no-op -- see the model's own doc comment.
func (r *jailSnapshotRollbackResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
}
