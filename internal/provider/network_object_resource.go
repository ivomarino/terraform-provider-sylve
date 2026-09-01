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
	_ resource.Resource                = &networkObjectResource{}
	_ resource.ResourceWithConfigure   = &networkObjectResource{}
	_ resource.ResourceWithImportState = &networkObjectResource{}
)

// NewNetworkObjectResource is the constructor registered in provider.Resources.
func NewNetworkObjectResource() resource.Resource {
	return &networkObjectResource{}
}

type networkObjectResource struct {
	client *sylveclient.Client
}

type networkObjectResourceModel struct {
	ID     types.String `tfsdk:"id"`
	Name   types.String `tfsdk:"name"`
	Type   types.String `tfsdk:"type"`
	Values types.List   `tfsdk:"values"`
}

func (r *networkObjectResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_network_object"
}

func (r *networkObjectResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A Sylve network object: a named, typed set of values -- MAC addresses, IPs/CIDRs, " +
			"hostnames, ports, countries, or a list of any of those. VM/jail mac_id and a standard " +
			"switch's network4/gateway4/etc. all reference one of these by ID (not yet wired into this " +
			"provider's own sylve_vm/sylve_jail resources).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Terraform resource identity -- the object's numeric ID as a string.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Object name. Must be unique across all network objects on the node.",
			},
			"type": schema.StringAttribute{
				Required: true,
				Description: "\"Host\" (hostname/FQDN), \"Mac\" (MAC address), \"Network\" (IP/CIDR), " +
					"\"Port\", \"Country\" (country code), or \"List\" (a list of references to other " +
					"objects). Not validated client-side; an invalid value is rejected server-side.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"values": schema.ListAttribute{
				ElementType: types.StringType,
				Required:    true,
				Description: "The object's values, e.g. [\"AA:BB:CC:DD:EE:FF\"] for a Mac object or " +
					"[\"192.168.1.0/24\"] for a Network object. Updated in place via EditNetworkObject " +
					"(full replace, not a diff/patch).",
			},
		},
	}
}

func (r *networkObjectResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func valuesFromList(ctx context.Context, l types.List) ([]string, error) {
	if l.IsNull() || l.IsUnknown() {
		return []string{}, nil
	}
	var out []string
	if diags := l.ElementsAs(ctx, &out, false); diags.HasError() {
		return nil, fmt.Errorf("decoding values list: %v", diags)
	}
	return out, nil
}

func (r *networkObjectResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan networkObjectResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	values, err := valuesFromList(ctx, plan.Values)
	if err != nil {
		resp.Diagnostics.AddError("Error decoding values", err.Error())
		return
	}

	id, err := r.client.CreateNetworkObject(ctx, plan.Name.ValueString(), plan.Type.ValueString(), values)
	if err != nil {
		resp.Diagnostics.AddError("Error creating Sylve network object", err.Error())
		return
	}

	plan.ID = types.StringValue(strconv.Itoa(id))
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *networkObjectResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state networkObjectResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, err := strconv.Atoi(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid state", fmt.Sprintf("id %q is not numeric: %s", state.ID.ValueString(), err))
		return
	}

	obj, err := r.client.GetNetworkObject(ctx, id)
	if err != nil {
		if sylveclient.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Sylve network object", err.Error())
		return
	}

	state.Name = types.StringValue(obj.Name)
	state.Type = types.StringValue(obj.Type)
	valuesList, diags := types.ListValueFrom(ctx, types.StringType, obj.Values)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	state.Values = valuesList

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *networkObjectResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state networkObjectResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	values, err := valuesFromList(ctx, plan.Values)
	if err != nil {
		resp.Diagnostics.AddError("Error decoding values", err.Error())
		return
	}

	id, err := strconv.Atoi(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid state", fmt.Sprintf("id %q is not numeric: %s", state.ID.ValueString(), err))
		return
	}

	if err := r.client.EditNetworkObject(ctx, id, plan.Name.ValueString(), plan.Type.ValueString(), values); err != nil {
		resp.Diagnostics.AddError("Error updating Sylve network object", err.Error())
		return
	}

	plan.ID = state.ID
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *networkObjectResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state networkObjectResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, err := strconv.Atoi(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid state", fmt.Sprintf("id %q is not numeric: %s", state.ID.ValueString(), err))
		return
	}

	if err := r.client.DeleteNetworkObject(ctx, id); err != nil && !sylveclient.IsNotFound(err) {
		resp.Diagnostics.AddError("Error deleting Sylve network object", err.Error())
	}
}

// ImportState accepts a bare numeric object ID.
func (r *networkObjectResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if _, err := strconv.Atoi(req.ID); err != nil {
		resp.Diagnostics.AddError(
			"Invalid import ID",
			fmt.Sprintf("Expected a numeric Sylve network object ID, got %q: %s", req.ID, err),
		)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
