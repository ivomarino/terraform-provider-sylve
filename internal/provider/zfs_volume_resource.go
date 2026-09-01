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
	_ resource.Resource                = &zfsVolumeResource{}
	_ resource.ResourceWithConfigure   = &zfsVolumeResource{}
	_ resource.ResourceWithImportState = &zfsVolumeResource{}
)

// NewZFSVolumeResource is the constructor registered in provider.Resources.
func NewZFSVolumeResource() resource.Resource {
	return &zfsVolumeResource{}
}

type zfsVolumeResource struct {
	client *sylveclient.Client
}

// zfsVolumeResourceModel: properties is write-only, identical reasoning
// to sylve_zfs_filesystem's own properties attribute -- see that
// resource's doc comment.
type zfsVolumeResourceModel struct {
	ID         types.String `tfsdk:"id"` // GUID
	Name       types.String `tfsdk:"name"`
	Parent     types.String `tfsdk:"parent"`
	Properties types.Map    `tfsdk:"properties"`
	Pool       types.String `tfsdk:"pool"`

	// FlashFromDownloadUUID is write-only and create-time-only: it
	// triggers a one-shot FlashVolume call right after the volume is
	// created, it isn't a property Sylve stores or reports back anywhere
	// (there's nothing in GET /zfs/datasets to say "this volume was
	// flashed from X"). Re-flashing an already-in-use volume on Update
	// isn't attempted -- changing this attribute recreates the resource
	// instead, which is far safer than silently overwriting a disk that
	// might be attached to a running VM.
	FlashFromDownloadUUID types.String `tfsdk:"flash_from_download_uuid"`
}

func (r *zfsVolumeResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_zfs_volume"
}

func (r *zfsVolumeResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A ZFS volume (zvol) managed through Sylve, e.g. for a VM's storage_type = \"zvol\" " +
			"disk. Unlike sylve_zfs_filesystem, Sylve's create API has no parent-field bug here -- see " +
			"the provider's dev notes for the contrast.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Terraform resource identity -- the volume's ZFS GUID.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:      true,
				Description:   "Leaf volume name, e.g. \"my-vol\" for parent \"tank\" -> full path \"tank/my-vol\". Immutable (no rename endpoint); changing it recreates the resource.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"parent": schema.StringAttribute{
				Required:      true,
				Description:   "Full path of the parent dataset/pool, e.g. \"tank\". Immutable.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"properties": schema.MapAttribute{
				ElementType: types.StringType,
				Required:    true,
				Description: "ZFS properties to set. MUST include \"size\" as a human-readable string " +
					"(e.g. {size = \"10G\"}) -- Sylve has no separate structured size field, and rejects " +
					"a create with no size property in this map at all. Write-only: not read back (same " +
					"reasoning as sylve_zfs_filesystem's own properties), and changes are applied via " +
					"Update (PATCH), not recreation.",
			},
			"pool": schema.StringAttribute{
				Computed:      true,
				Description:   "Pool the volume lives on, as reported by Sylve (derived from parent).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"flash_from_download_uuid": schema.StringAttribute{
				Optional: true,
				Description: "UUID of a sylve_download (typically a cloud image, automatic_raw_conversion " +
					"= true) to write onto this volume immediately after it's created -- the actual " +
					"mechanism for turning a downloaded cloud image into VM-attachable disk contents; " +
					"see sylve_vm_storage for attaching the result to a VM. Write-only and create-time " +
					"only: changing it recreates the volume rather than re-flashing a volume that might " +
					"already be attached to a running VM.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
		},
	}
}

func (r *zfsVolumeResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *zfsVolumeResource) propertiesMap(ctx context.Context, plan zfsVolumeResourceModel) (map[string]string, error) {
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

func (r *zfsVolumeResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan zfsVolumeResourceModel
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
	if err := r.client.CreateVolume(ctx, name, parent, props); err != nil {
		resp.Diagnostics.AddError("Error creating Sylve ZFS volume", err.Error())
		return
	}

	fullName := parent + "/" + name
	created, err := r.client.GetDatasetByName(ctx, fullName)
	if err != nil {
		resp.Diagnostics.AddError("Sylve ZFS volume created but could not be looked up afterwards", err.Error())
		return
	}

	plan.ID = types.StringValue(created.GUID)
	plan.Pool = types.StringValue(created.Pool)

	if uuid := plan.FlashFromDownloadUUID.ValueString(); uuid != "" {
		if err := r.client.FlashVolume(ctx, created.GUID, uuid); err != nil {
			// The volume itself was created successfully -- record that
			// in state before returning the error, so a retry (or
			// `terraform destroy`) has something real to act on instead
			// of Terraform believing Create failed outright and losing
			// track of the volume it did create.
			resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
			resp.Diagnostics.AddError("Sylve ZFS volume created but flashing it failed", err.Error())
			return
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *zfsVolumeResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state zfsVolumeResourceModel
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
		resp.Diagnostics.AddError("Error reading Sylve ZFS volume", err.Error())
		return
	}

	state.Pool = types.StringValue(ds.Pool)
	// name/parent/properties are deliberately left untouched -- see the
	// schema's own descriptions.
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *zfsVolumeResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state zfsVolumeResourceModel
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
		if err := r.client.EditVolume(ctx, state.ID.ValueString(), props); err != nil {
			resp.Diagnostics.AddError("Error updating Sylve ZFS volume properties", err.Error())
			return
		}
	}

	plan.ID = state.ID
	plan.Pool = state.Pool
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *zfsVolumeResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state zfsVolumeResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.DeleteVolume(ctx, state.ID.ValueString()); err != nil && !sylveclient.IsNotFound(err) {
		resp.Diagnostics.AddError("Error deleting Sylve ZFS volume", err.Error())
	}
}

// ImportState accepts a bare GUID.
func (r *zfsVolumeResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
