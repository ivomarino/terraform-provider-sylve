package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ivomarino/terraform-provider-sylve/internal/sylveclient"
)

var (
	_ resource.Resource                = &zfsFilesystemResource{}
	_ resource.ResourceWithConfigure   = &zfsFilesystemResource{}
	_ resource.ResourceWithImportState = &zfsFilesystemResource{}
)

// NewZFSFilesystemResource is the constructor registered in provider.Resources.
func NewZFSFilesystemResource() resource.Resource {
	return &zfsFilesystemResource{}
}

type zfsFilesystemResource struct {
	client *sylveclient.Client
}

// zfsFilesystemResourceModel: properties is write-only, like sylve_vm's
// storage_pool/switch_name -- Sylve's GET /zfs/datasets response carries
// every ZFS property (recordsize, compression, quota, ...) each tagged
// with its own LOCAL/INHERITED/DEFAULT/NONE source, and there's no clean
// way to map "the handful of properties this resource set" back out of
// that without either fighting inherited-value noise or silently
// dropping properties a previous apply set and a later one didn't
// mention. Simpler and more honest to not read it back at all: set once
// at create (or via Update), never verified against drift.
type zfsFilesystemResourceModel struct {
	ID         types.String `tfsdk:"id"` // GUID
	Name       types.String `tfsdk:"name"`
	Parent     types.String `tfsdk:"parent"`
	Properties types.Map    `tfsdk:"properties"`
	Pool       types.String `tfsdk:"pool"`
	Mountpoint types.String `tfsdk:"mountpoint"`
}

func (r *zfsFilesystemResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_zfs_filesystem"
}

func (r *zfsFilesystemResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A ZFS filesystem dataset managed through Sylve, e.g. for a VM/jail storage pool " +
			"or a Samba share. Does not cover ZFS volumes (zvols) or pools themselves -- see the " +
			"provider's dev notes.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Terraform resource identity -- the dataset's ZFS GUID.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:      true,
				Description:   "Leaf dataset name, e.g. \"my-fs\" for parent \"tank\" -> full path \"tank/my-fs\". Immutable (no rename endpoint); changing it recreates the resource.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"parent": schema.StringAttribute{
				Required: true,
				Description: "Full path of the parent dataset/pool, e.g. \"tank\" or \"tank/sylve\". " +
					"Sylve's own create API has a real quirk here (see the provider's dev notes): its " +
					"top-level \"parent\" request field is validated but then silently discarded -- the " +
					"value actually used is read back out of a same-named key inside \"properties\" " +
					"instead. This provider sends both so the practitioner never needs to know that.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"properties": schema.MapAttribute{
				ElementType: types.StringType,
				Optional:    true,
				Description: "ZFS properties to set, e.g. {compression = \"lz4\", quota = \"10G\"}. " +
					"Write-only: not read back (see resource description), and changing this attribute " +
					"is applied via Update (PATCH), not recreation -- but Terraform can't detect drift " +
					"if something else changes a property out-of-band.",
			},
			"pool": schema.StringAttribute{
				Computed:      true,
				Description:   "Pool the dataset lives on, as reported by Sylve (derived from parent).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"mountpoint": schema.StringAttribute{
				Computed:      true,
				Description:   "Filesystem mountpoint, as reported by Sylve.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *zfsFilesystemResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *zfsFilesystemResource) propertiesMap(ctx context.Context, plan zfsFilesystemResourceModel) (map[string]string, error) {
	props := make(map[string]string)
	if plan.Properties.IsNull() || plan.Properties.IsUnknown() {
		return props, nil
	}
	elements := make(map[string]types.String, len(plan.Properties.Elements()))
	if diags := plan.Properties.ElementsAs(ctx, &elements, false); diags.HasError() {
		return nil, fmt.Errorf("decoding properties map: %v", diags)
	}
	for k, v := range elements {
		props[k] = v.ValueString()
	}
	return props, nil
}

func (r *zfsFilesystemResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan zfsFilesystemResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	props, err := r.propertiesMap(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Error decoding properties", err.Error())
		return
	}

	name := plan.Name.ValueString()
	parent := plan.Parent.ValueString()
	if err := r.client.CreateFilesystem(ctx, name, parent, props); err != nil {
		resp.Diagnostics.AddError("Error creating Sylve ZFS filesystem", err.Error())
		return
	}

	fullName := parent + "/" + name
	created, err := r.client.GetDatasetByName(ctx, fullName)
	if err != nil {
		resp.Diagnostics.AddError("Sylve ZFS filesystem created but could not be looked up afterwards", err.Error())
		return
	}

	plan.ID = types.StringValue(created.GUID)
	plan.Pool = types.StringValue(created.Pool)
	plan.Mountpoint = types.StringValue(created.Mountpoint)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *zfsFilesystemResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state zfsFilesystemResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	ds, err := r.client.GetDatasetByGUID(ctx, state.ID.ValueString())
	if err != nil {
		if sylveclient.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Sylve ZFS filesystem", err.Error())
		return
	}

	state.Pool = types.StringValue(ds.Pool)
	state.Mountpoint = types.StringValue(ds.Mountpoint)
	// name/parent/properties are deliberately left untouched -- see the
	// schema's own descriptions.
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *zfsFilesystemResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state zfsFilesystemResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	props, err := r.propertiesMap(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Error decoding properties", err.Error())
		return
	}
	if len(props) > 0 {
		if err := r.client.EditFilesystem(ctx, state.ID.ValueString(), props); err != nil {
			resp.Diagnostics.AddError("Error updating Sylve ZFS filesystem properties", err.Error())
			return
		}
	}

	plan.ID = state.ID
	plan.Pool = state.Pool
	plan.Mountpoint = state.Mountpoint
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *zfsFilesystemResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state zfsFilesystemResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.DeleteFilesystem(ctx, state.ID.ValueString()); err != nil && !sylveclient.IsNotFound(err) {
		resp.Diagnostics.AddError("Error deleting Sylve ZFS filesystem", err.Error())
	}
}

// ImportState accepts a bare GUID. name/parent/properties come back
// empty on an imported resource (see Read's own comment) -- reconcile
// config to match reality before applying, same as sylve_vm's
// write-only attributes.
func (r *zfsFilesystemResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
