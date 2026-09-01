package provider

import (
	"context"
	"fmt"
	"strconv"
	"time"

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

// downloadWaitTimeout bounds how long Create will poll a download before
// giving up. Generous on purpose -- a real ISO or cloud image over a home
// LAN can take a while -- but not infinite, so a genuinely stuck download
// doesn't hang `terraform apply` forever.
const downloadWaitTimeout = 30 * time.Minute

var (
	_ resource.Resource                = &downloadResource{}
	_ resource.ResourceWithConfigure   = &downloadResource{}
	_ resource.ResourceWithImportState = &downloadResource{}
)

// NewDownloadResource is the constructor registered in provider.Resources.
func NewDownloadResource() resource.Resource {
	return &downloadResource{}
}

type downloadResource struct {
	client *sylveclient.Client
}

type downloadResourceModel struct {
	ID                     types.String `tfsdk:"id"` // numeric download ID
	URL                    types.String `tfsdk:"url"`
	Filename               types.String `tfsdk:"filename"`
	UType                  types.String `tfsdk:"utype"`
	IgnoreTLS              types.Bool   `tfsdk:"ignore_tls"`
	AutomaticExtraction    types.Bool   `tfsdk:"automatic_extraction"`
	AutomaticRawConversion types.Bool   `tfsdk:"automatic_raw_conversion"`
	UUID                   types.String `tfsdk:"uuid"`
	Type                   types.String `tfsdk:"type"`
	Path                   types.String `tfsdk:"path"`
	Size                   types.Int64  `tfsdk:"size"`
}

func (r *downloadResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_download"
}

func (r *downloadResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}

	resp.Schema = schema.Schema{
		Description: "A file Sylve has fetched or registered for use as VM/jail install media: an ISO, " +
			"a cloud image, or a jail base rootfs archive. Referenced by sylve_vm's iso attribute (not " +
			"yet implemented in this provider) via this resource's uuid. `terraform apply` blocks until " +
			"the download reaches a terminal state (up to 30 minutes) -- there is no way to make this " +
			"asynchronous within a single apply.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Terraform resource identity -- the download's numeric ID as a string.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"url": schema.StringAttribute{
				Required: true,
				Description: "Source, auto-classified by Sylve from its shape: a magnet URI becomes a " +
					"torrent download, an http(s) URL becomes an HTTP download, and an absolute " +
					"filesystem path already present on the Sylve host is copied/registered locally with " +
					"no network fetch at all. Immutable; changing it recreates the resource (there is no " +
					"update endpoint for a download's source).",
				PlanModifiers: replace,
			},
			"filename": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Destination filename. Defaults to the URL/path's own basename. Immutable.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplace()},
			},
			"utype": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Description: "\"base-rootfs\" (jail base archive), \"cloud-init\" (cloud-init-capable " +
					"image, referenceable by a VM with cloud_init enabled), or \"uncategoried\" (default " +
					"-- Sylve's own spelling, not a typo introduced here; not validated client-side, same " +
					"as this provider's other enum-shaped attributes -- an invalid value is rejected " +
					"server-side instead). Immutable.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplace()},
			},
			"ignore_tls": schema.BoolAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Skip TLS certificate verification for an https:// url. Immutable.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown(), boolplanmodifier.RequiresReplace()},
			},
			"automatic_extraction": schema.BoolAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Automatically extract a compressed download (e.g. .xz/.gz) after fetch. Immutable.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown(), boolplanmodifier.RequiresReplace()},
			},
			"automatic_raw_conversion": schema.BoolAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Automatically convert a cloud image (e.g. qcow2) to raw after fetch. Immutable.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown(), boolplanmodifier.RequiresReplace()},
			},
			"uuid": schema.StringAttribute{
				Computed:      true,
				Description:   "Sylve's UUID for this download -- what a VM's iso attribute references.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"type": schema.StringAttribute{
				Computed:      true,
				Description:   "Server-detected source kind: \"http\", \"torrent\", or \"path\".",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"path": schema.StringAttribute{
				Computed:      true,
				Description:   "Final file path on the Sylve host.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"size": schema.Int64Attribute{
				Computed:      true,
				Description:   "File size in bytes, once known.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *downloadResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *downloadResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan downloadResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	uType := plan.UType.ValueString()
	if uType == "" {
		uType = "uncategoried"
	}
	url := plan.URL.ValueString()

	if err := r.client.CreateDownload(ctx, url, plan.Filename.ValueString(), uType,
		plan.IgnoreTLS.ValueBool(), plan.AutomaticExtraction.ValueBool(), plan.AutomaticRawConversion.ValueBool(),
	); err != nil {
		resp.Diagnostics.AddError("Error starting Sylve download", err.Error())
		return
	}

	created, err := r.client.WaitForDownload(ctx, url, downloadWaitTimeout)
	if err != nil {
		resp.Diagnostics.AddError("Sylve download did not complete", err.Error())
		return
	}

	plan.ID = types.StringValue(strconv.Itoa(created.ID))
	plan.Filename = types.StringValue(created.Name)
	plan.UType = types.StringValue(created.UType)
	plan.IgnoreTLS = types.BoolValue(created.IgnoreTLS)
	plan.AutomaticExtraction = types.BoolValue(created.AutomaticExtraction)
	plan.AutomaticRawConversion = types.BoolValue(created.AutomaticRawConversion)
	plan.UUID = types.StringValue(created.UUID)
	plan.Type = types.StringValue(created.Type)
	plan.Path = types.StringValue(created.Path)
	plan.Size = types.Int64Value(created.Size)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *downloadResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state downloadResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, err := strconv.Atoi(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid state", fmt.Sprintf("id %q is not numeric: %s", state.ID.ValueString(), err))
		return
	}

	d, err := r.client.GetDownloadByID(ctx, id)
	if err != nil {
		if sylveclient.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Sylve download", err.Error())
		return
	}

	state.URL = types.StringValue(d.URL)
	state.Filename = types.StringValue(d.Name)
	state.UType = types.StringValue(d.UType)
	state.IgnoreTLS = types.BoolValue(d.IgnoreTLS)
	state.AutomaticExtraction = types.BoolValue(d.AutomaticExtraction)
	state.AutomaticRawConversion = types.BoolValue(d.AutomaticRawConversion)
	state.UUID = types.StringValue(d.UUID)
	state.Type = types.StringValue(d.Type)
	state.Path = types.StringValue(d.Path)
	state.Size = types.Int64Value(d.Size)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update is never reached in practice: every attribute forces
// replacement -- but the interface requires an implementation.
func (r *downloadResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan downloadResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *downloadResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state downloadResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, err := strconv.Atoi(state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid state", fmt.Sprintf("id %q is not numeric: %s", state.ID.ValueString(), err))
		return
	}

	if err := r.client.DeleteDownload(ctx, id); err != nil && !sylveclient.IsNotFound(err) {
		resp.Diagnostics.AddError("Error deleting Sylve download", err.Error())
	}
}

// ImportState accepts a bare numeric download ID.
func (r *downloadResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if _, err := strconv.Atoi(req.ID); err != nil {
		resp.Diagnostics.AddError(
			"Invalid import ID",
			fmt.Sprintf("Expected a numeric Sylve download ID, got %q: %s", req.ID, err),
		)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
