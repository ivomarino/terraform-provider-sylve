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
	_ resource.Resource              = &vmSnapshotRollbackResource{}
	_ resource.ResourceWithConfigure = &vmSnapshotRollbackResource{}
)

// NewVMSnapshotRollbackResource is the constructor registered in provider.Resources.
func NewVMSnapshotRollbackResource() resource.Resource {
	return &vmSnapshotRollbackResource{}
}

type vmSnapshotRollbackResource struct {
	client *sylveclient.Client
}

// vmSnapshotRollbackResourceModel: this resource has no real persistent
// state -- a rollback is a one-shot action, not a thing that exists to
// be read back or "un-done". It follows the same convention as
// hashicorp/null's null_resource with `triggers`: Create fires the
// action; Read is a no-op that trusts whatever's already in state (there
// is nothing external to verify -- the rollback either happened or it
// didn't, and by the time Read runs, it did); Delete is a no-op (nothing
// to undo); Update is unreachable because `trigger` is the only
// non-Computed attribute and it forces replacement.
type vmSnapshotRollbackResourceModel struct {
	ID         types.String `tfsdk:"id"`
	RID        types.Int64  `tfsdk:"rid"`
	SnapshotID types.Int64  `tfsdk:"snapshot_id"`
	Trigger    types.String `tfsdk:"trigger"`
}

func (r *vmSnapshotRollbackResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vm_snapshot_rollback"
}

func (r *vmSnapshotRollbackResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Rolls a sylve_vm back to a prior sylve_vm_snapshot. This is a one-shot ACTION, " +
			"not persistent state -- modeled the same way hashicorp/null's null_resource models " +
			"triggered actions. Applying this resource for the first time performs the rollback " +
			"immediately; nothing happens again unless `trigger` changes, which recreates the resource " +
			"and fires the rollback again (e.g. bump trigger to a new timestamp/uuid to roll back to the " +
			"same snapshot a second time -- Terraform has no way to know you want that from unchanged " +
			"config alone). destroying this resource does NOT undo anything; there is no \"rollback the " +
			"rollback\" -- if you need that, take a new sylve_vm_snapshot before rolling back. Sylve " +
			"itself hardcodes destroying any newer state as part of the rollback -- there is no partial " +
			"or non-destructive rollback option server-side.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Terraform resource identity. Not a real external ID -- just \"<rid>-<snapshot_id>\" for display.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"rid": schema.Int64Attribute{
				Required:      true,
				Description:   "RID of the sylve_vm to roll back.",
				PlanModifiers: []planmodifier.Int64{},
			},
			"snapshot_id": schema.Int64Attribute{
				Required:      true,
				Description:   "ID of the sylve_vm_snapshot to roll back to.",
				PlanModifiers: []planmodifier.Int64{},
			},
			"trigger": schema.StringAttribute{
				Required: true,
				Description: "Arbitrary string. Changing it re-fires the rollback (see the resource's " +
					"own description for why this is necessary -- there is no other way to ask Terraform " +
					"to repeat a one-shot action against otherwise-unchanged config).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
		},
	}
}

func (r *vmSnapshotRollbackResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *vmSnapshotRollbackResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan vmSnapshotRollbackResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	rid := int(plan.RID.ValueInt64())
	snapshotID := int(plan.SnapshotID.ValueInt64())
	if err := r.client.RollbackVMSnapshot(ctx, rid, snapshotID); err != nil {
		resp.Diagnostics.AddError("Error rolling back Sylve VM snapshot", err.Error())
		return
	}

	plan.ID = types.StringValue(fmt.Sprintf("%d-%d", rid, snapshotID))
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read is deliberately a no-op past the initial Get/Set -- see the
// model's own doc comment.
func (r *vmSnapshotRollbackResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state vmSnapshotRollbackResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update is unreachable: `trigger` is the only attribute that isn't
// Computed, and it forces replacement.
func (r *vmSnapshotRollbackResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan vmSnapshotRollbackResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete is deliberately a no-op -- see the model's own doc comment.
func (r *vmSnapshotRollbackResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
}
