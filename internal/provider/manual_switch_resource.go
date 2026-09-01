package provider

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ivomarino/terraform-provider-sylve/internal/sylveclient"
)

var (
	_ resource.Resource                = &manualSwitchResource{}
	_ resource.ResourceWithConfigure   = &manualSwitchResource{}
	_ resource.ResourceWithImportState = &manualSwitchResource{}
)

// NewManualSwitchResource is the constructor registered in provider.Resources.
func NewManualSwitchResource() resource.Resource {
	return &manualSwitchResource{}
}

type manualSwitchResource struct {
	client *sylveclient.Client
}

type manualSwitchResourceModel struct {
	ID     types.String `tfsdk:"id"`
	Name   types.String `tfsdk:"name"`
	Bridge types.String `tfsdk:"bridge"`
}

func (r *manualSwitchResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_manual_switch"
}

func (r *manualSwitchResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A Sylve manual network switch: a plain bridge interface with no physical port " +
			"bound to it. Safe to create/destroy on any host, unlike a \"standard\" switch (not covered " +
			"by this provider) which requires binding a real physical NIC and can sever connectivity on " +
			"a single-NIC host -- see the provider's dev notes. VMs/jails reference this by name via " +
			"their own switch_name attribute.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Terraform resource identity -- the switch's numeric ID as a string.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required: true,
				Description: "Switch name, referenced by VM/jail switch_name attributes. Immutable: " +
					"Sylve's API has no update endpoint for manual switches (only create/delete), so " +
					"changing this recreates the resource.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"bridge": schema.StringAttribute{
				Required:      true,
				Description:   "Underlying FreeBSD bridge interface name, e.g. \"bridge10\". Immutable, same reason as name.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
		},
	}
}

func (r *manualSwitchResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *manualSwitchResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan manualSwitchResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name := plan.Name.ValueString()
	if err := r.client.CreateManualSwitch(ctx, name, plan.Bridge.ValueString()); err != nil {
		resp.Diagnostics.AddError("Error creating Sylve manual switch", err.Error())
		return
	}

	created, err := r.client.GetManualSwitchByName(ctx, name)
	if err != nil {
		resp.Diagnostics.AddError("Sylve manual switch created but could not be looked up afterwards", err.Error())
		return
	}

	plan.ID = types.StringValue(strconv.Itoa(created.ID))
	plan.Bridge = types.StringValue(created.Bridge)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *manualSwitchResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state manualSwitchResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, err := strconv.Atoi(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid state", fmt.Sprintf("id %q is not numeric: %s", state.ID.ValueString(), err))
		return
	}

	sw, err := r.client.GetManualSwitch(ctx, id)
	if err != nil {
		if sylveclient.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Sylve manual switch", err.Error())
		return
	}

	state.Name = types.StringValue(sw.Name)
	state.Bridge = types.StringValue(sw.Bridge)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update is never reached in practice: name and bridge both force
// replacement, and there's nothing else in the schema -- but the
// interface requires an implementation.
func (r *manualSwitchResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan manualSwitchResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *manualSwitchResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state manualSwitchResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, err := strconv.Atoi(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid state", fmt.Sprintf("id %q is not numeric: %s", state.ID.ValueString(), err))
		return
	}

	if err := r.client.DeleteManualSwitch(ctx, id); err != nil && !sylveclient.IsNotFound(err) {
		resp.Diagnostics.AddError("Error deleting Sylve manual switch", err.Error())
	}
}

// ImportState accepts a bare numeric switch ID.
func (r *manualSwitchResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if _, err := strconv.Atoi(req.ID); err != nil {
		resp.Diagnostics.AddError(
			"Invalid import ID",
			fmt.Sprintf("Expected a numeric Sylve manual switch ID, got %q: %s", req.ID, err),
		)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
