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
	_ resource.Resource                = &vmNetworkResource{}
	_ resource.ResourceWithConfigure   = &vmNetworkResource{}
	_ resource.ResourceWithImportState = &vmNetworkResource{}
)

// NewVMNetworkResource is the constructor registered in provider.Resources.
func NewVMNetworkResource() resource.Resource {
	return &vmNetworkResource{}
}

type vmNetworkResource struct {
	client *sylveclient.Client
}

type vmNetworkResourceModel struct {
	ID         types.String `tfsdk:"id"` // this network entry's own ID (NOT the VM's rid)
	RID        types.Int64  `tfsdk:"rid"`
	SwitchName types.String `tfsdk:"switch_name"`
	Emulation  types.String `tfsdk:"emulation"`
	MacID      types.Int64  `tfsdk:"mac_id"`
}

func (r *vmNetworkResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vm_network"
}

func (r *vmNetworkResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A NIC attached to a sylve_vm beyond its create-time first NIC (sylve_vm's own " +
			"switch_name/switch_emulation_type/mac_id). Unlike sylve_vm_storage, no shutoff requirement " +
			"-- NIC attach/detach/update work on a running VM.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Terraform resource identity -- this network entry's own numeric ID (distinct from the VM's rid).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"rid": schema.Int64Attribute{
				Required:      true,
				Description:   "RID of the sylve_vm to attach this NIC to. Immutable -- moving a NIC between VMs isn't supported by this resource.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"switch_name": schema.StringAttribute{
				Required:    true,
				Description: "Name of an existing Sylve network switch to attach to. Updatable in place via UpdateNetwork.",
			},
			"emulation": schema.StringAttribute{
				Required: true,
				Description: "NIC emulation type, e.g. \"virtio\" (confirmed against a live VM's own " +
					"network entry -- not the same value space as sylve_vm_storage's \"virtio-blk\"). " +
					"Updatable in place.",
			},
			"mac_id": schema.Int64Attribute{
				Optional: true,
				Computed: true,
				Description: "ID of an existing sylve_network_object (type \"Mac\") to use, instead of " +
					"letting Sylve auto-generate one. Updatable in place.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *vmNetworkResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *vmNetworkResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan vmNetworkResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	rid := int(plan.RID.ValueInt64())
	switchName := plan.SwitchName.ValueString()
	emulation := plan.Emulation.ValueString()
	macID := int(plan.MacID.ValueInt64())

	if err := r.client.AttachNetwork(ctx, rid, switchName, emulation, macID); err != nil {
		resp.Diagnostics.AddError("Error attaching Sylve VM network", err.Error())
		return
	}

	created, err := r.client.FindVMNetwork(ctx, rid, switchName, emulation)
	if err != nil {
		resp.Diagnostics.AddError("Sylve VM network attached but could not be looked up afterwards", err.Error())
		return
	}

	plan.ID = types.StringValue(strconv.Itoa(created.ID))
	plan.MacID = types.Int64Value(int64(created.MacID))
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *vmNetworkResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state vmNetworkResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, err := strconv.Atoi(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid state", fmt.Sprintf("id %q is not numeric: %s", state.ID.ValueString(), err))
		return
	}

	net, err := r.client.GetVMNetwork(ctx, int(state.RID.ValueInt64()), id)
	if err != nil {
		if sylveclient.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Sylve VM network", err.Error())
		return
	}

	state.SwitchName = types.StringValue(net.SwitchName)
	state.Emulation = types.StringValue(net.Emulation)
	state.MacID = types.Int64Value(int64(net.MacID))
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *vmNetworkResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state vmNetworkResourceModel
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

	err = r.client.UpdateNetwork(ctx, id, plan.SwitchName.ValueString(), plan.Emulation.ValueString(), int(plan.MacID.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Error updating Sylve VM network", err.Error())
		return
	}

	plan.ID = state.ID
	plan.RID = state.RID
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *vmNetworkResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state vmNetworkResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, err := strconv.Atoi(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid state", fmt.Sprintf("id %q is not numeric: %s", state.ID.ValueString(), err))
		return
	}

	err = r.client.DetachNetwork(ctx, int(state.RID.ValueInt64()), id)
	if err != nil && !sylveclient.IsNotFound(err) {
		resp.Diagnostics.AddError("Error detaching Sylve VM network", err.Error())
	}
}

// ImportState accepts "<rid>/<networkId>", same two-part shape as
// sylve_vm_storage/sylve_vm_snapshot and for the same reason: no
// per-NIC GET endpoint, only the per-VM nested view.
func (r *vmNetworkResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.SplitN(req.ID, "/", 2)
	if len(parts) != 2 {
		resp.Diagnostics.AddError(
			"Invalid import ID",
			fmt.Sprintf("Expected \"<rid>/<networkId>\", e.g. \"10/1\", got %q", req.ID),
		)
		return
	}
	rid, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("rid %q is not numeric: %s", parts[0], err))
		return
	}
	if _, err := strconv.Atoi(parts[1]); err != nil {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("networkId %q is not numeric: %s", parts[1], err))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("rid"), rid)...)
}
