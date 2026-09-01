package provider

import (
	"context"
	"fmt"
	"strconv"

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

var (
	_ resource.Resource                = &jailResource{}
	_ resource.ResourceWithConfigure   = &jailResource{}
	_ resource.ResourceWithImportState = &jailResource{}
)

// NewJailResource is the constructor registered in provider.Resources.
func NewJailResource() resource.Resource {
	return &jailResource{}
}

type jailResource struct {
	client *sylveclient.Client
}

// jailResourceModel: structurally parallel to vmResourceModel. pool/base
// are write-only-at-create -- Sylve provisions the jail's root dataset
// from `base` once, at creation, and there's no "reattach a different
// base" update endpoint, nor does GET /jail/:id echo `pool`/`base` back
// flat (same shape of gap as sylve_vm's storage_pool/switch_name).
type jailResourceModel struct {
	ID          types.String `tfsdk:"id"` // string form of ctid
	DBID        types.Int64  `tfsdk:"db_id"`
	CTID        types.Int64  `tfsdk:"ctid"`
	Name        types.String `tfsdk:"name"`
	Hostname    types.String `tfsdk:"hostname"`
	Description types.String `tfsdk:"description"`
	Type        types.String `tfsdk:"type"`

	Pool       types.String `tfsdk:"pool"`
	Base       types.String `tfsdk:"base"`
	SwitchName types.String `tfsdk:"switch_name"`

	Cores          types.Int64 `tfsdk:"cores"`
	Memory         types.Int64 `tfsdk:"memory"`
	ResourceLimits types.Bool  `tfsdk:"resource_limits"`
	StartAtBoot    types.Bool  `tfsdk:"start_at_boot"`
	StartOrder     types.Int64 `tfsdk:"start_order"`

	CleanEnvironment  types.Bool   `tfsdk:"clean_environment"`
	AdditionalOptions types.String `tfsdk:"additional_options"`
	DevFSRuleset      types.String `tfsdk:"devfs_ruleset"`
	Fstab             types.String `tfsdk:"fstab"`
	ResolvConf        types.String `tfsdk:"resolv_conf"`
}

func (r *jailResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_jail"
}

func (r *jailResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	sticky := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}

	resp.Schema = schema.Schema{
		Description: "A FreeBSD or Linux jail managed by Sylve. Structurally parallel to sylve_vm: " +
			"covers the jail object itself (CPU/memory/boot options) and its base filesystem/switch, set " +
			"at creation time. Additional storage/network attachments, cloud-init-equivalent metadata, " +
			"and lifecycle hooks are not yet covered -- see the provider's dev notes.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Terraform resource identity -- the jail's CTID as a string.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"db_id": schema.Int64Attribute{
				Computed: true,
				Description: "Sylve's own internal primary key for this jail -- NOT the same as ctid. " +
					"Exposed only because SetJailName/SetJailDescription genuinely require it instead " +
					"of ctid, unlike every other jail (and every VM) update endpoint; not meant to be " +
					"referenced from other configuration.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"ctid": schema.Int64Attribute{
				Required: true,
				Description: "Sylve's container ID for this jail, 1-9999, unique across jails. Like " +
					"sylve_vm's rid, this is NOT auto-assigned -- ValidateCreate rejects ctid <= 0 " +
					"outright with invalid_ct_id. The caller must pick one.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Jail name.",
			},
			"hostname": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Jail hostname. Defaults to Sylve's own default (empty in config becomes server-assigned) if unset.",
				PlanModifiers: sticky,
			},
			"description": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Free-text description shown in the Sylve UI.",
				PlanModifiers: sticky,
			},
			"type": schema.StringAttribute{
				Required:      true,
				Description:   "\"freebsd\" or \"linux\". Immutable.",
				PlanModifiers: replace,
			},
			// pool/base/switch_name are all REQUIRED by Sylve's own
			// ValidateCreate (unlike sylve_vm's equivalents, which are
			// genuinely optional server-side) -- but they're schema
			// Optional here anyway, deliberately, matching sylve_vm's
			// own write-only fields. They're write-only (GET /jail/:id
			// never echoes them back flat), so an imported jail always
			// has them null in state; a schema-Required attribute can
			// never be omitted from config, so an imported jail's config
			// would be forced to carry a guessed value that plans as a
			// permanent replacement. Optional lets a practitioner omit
			// them entirely for an already-imported jail they don't
			// intend to ever recreate, same documented workaround as
			// sylve_vm's storage_pool/switch_name (see this resource's
			// own ImportState comment). Confirmed live, 2026-08-31 (see
			// the provider's dev notes) -- caught by testing import
			// against a real jail with these values actually declared in
			// config, which is exactly the case Required broke. Omitting
			// them from config on a genuine (non-imported) create still
			// fails clearly, just server-side instead of client-side --
			// pool_not_found / download_uuid_required /
			// switch_name_required.
			"pool": schema.StringAttribute{
				Optional:      true,
				Description:   "ZFS pool the jail's root filesystem is created on, e.g. \"tank\". Required by Sylve itself, but not enforced client-side -- see the comment above. Write-only, immutable.",
				PlanModifiers: replace,
			},
			"base": schema.StringAttribute{
				Optional: true,
				Description: "UUID of a sylve_download whose utype is \"base-rootfs\" and which has " +
					"already been extracted (automatic_extraction = true on that download). Required by " +
					"Sylve itself, but not enforced client-side -- see the comment above. Write-only, immutable.",
				PlanModifiers: replace,
			},
			"switch_name": schema.StringAttribute{
				Optional: true,
				Description: "Name of an existing Sylve network switch to attach to, or the literal " +
					"string \"none\" (no network) or \"inherit\" (inherit the host's own IPv4/IPv6). " +
					"Required by Sylve itself, but not enforced client-side -- see the comment above. " +
					"Write-only, immutable.",
				PlanModifiers: replace,
			},
			"cores": schema.Int64Attribute{
				Optional:      true,
				Computed:      true,
				Description:   "CPU core limit. No enforced minimum (unlike sylve_vm's cpu_* attributes). Changing it updates in place via SetJailCPU.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"memory": schema.Int64Attribute{
				Optional:      true,
				Computed:      true,
				Description:   "Memory limit in BYTES, same convention as sylve_vm's ram (confirmed against source, not MiB despite an initial wrong guess while writing this resource -- see the provider's dev notes). Changing it updates in place via SetJailMemory.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"resource_limits": schema.BoolAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Whether cores/memory limits are actually enforced (via rctl). Write-only: no dedicated update endpoint found; changing it recreates the jail.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown(), boolplanmodifier.RequiresReplace()},
			},
			"start_at_boot": schema.BoolAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Start this jail automatically when Sylve/the host boots. Write-only, changing it recreates the jail (same gap as sylve_vm's own start_at_boot).",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown(), boolplanmodifier.RequiresReplace()},
			},
			"start_order": schema.Int64Attribute{
				Optional:      true,
				Computed:      true,
				Description:   "Boot order among auto-started guests. Must be >= 0. Write-only, changing it recreates the jail.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown(), int64planmodifier.RequiresReplace()},
			},
			"clean_environment": schema.BoolAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Start the jail with a clean environment (clearenv). Write-only, immutable.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown(), boolplanmodifier.RequiresReplace()},
			},
			"additional_options": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Extra raw jail.conf options. Write-only, immutable.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplace()},
			},
			"devfs_ruleset": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "devfs ruleset name/number. Write-only, immutable.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplace()},
			},
			"fstab": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Extra fstab content. Write-only, immutable (Sylve has a dedicated ModifyFstab endpoint this provider doesn't call yet).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplace()},
			},
			"resolv_conf": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Jail-internal resolv.conf content. Write-only, immutable (Sylve has a dedicated ModifyResolvConf endpoint this provider doesn't call yet).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplace()},
			},
		},
	}
}

func (r *jailResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *jailResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan jailResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	created, err := r.client.CreateJail(ctx, sylveclient.Jail{
		Name:              plan.Name.ValueString(),
		CTID:              int(plan.CTID.ValueInt64()),
		Hostname:          plan.Hostname.ValueString(),
		Description:       plan.Description.ValueString(),
		Type:              plan.Type.ValueString(),
		Pool:              plan.Pool.ValueString(),
		Base:              plan.Base.ValueString(),
		SwitchName:        plan.SwitchName.ValueString(),
		Cores:             int(plan.Cores.ValueInt64()),
		Memory:            int(plan.Memory.ValueInt64()),
		ResourceLimits:    plan.ResourceLimits.ValueBool(),
		StartAtBoot:       plan.StartAtBoot.ValueBool(),
		StartOrder:        int(plan.StartOrder.ValueInt64()),
		CleanEnvironment:  plan.CleanEnvironment.ValueBool(),
		AdditionalOptions: plan.AdditionalOptions.ValueString(),
		DevFSRuleset:      plan.DevFSRuleset.ValueString(),
		Fstab:             plan.Fstab.ValueString(),
		ResolvConf:        plan.ResolvConf.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Error creating Sylve jail", err.Error())
		return
	}

	plan.ID = types.StringValue(strconv.Itoa(created.CTID))
	plan.DBID = types.Int64Value(int64(created.DBID))
	plan.Hostname = types.StringValue(created.Hostname)
	plan.Description = types.StringValue(created.Description)
	plan.Cores = types.Int64Value(int64(created.Cores))
	plan.Memory = types.Int64Value(int64(created.Memory))
	plan.ResourceLimits = types.BoolValue(created.ResourceLimits)
	plan.StartAtBoot = types.BoolValue(created.StartAtBoot)
	plan.StartOrder = types.Int64Value(int64(created.StartOrder))
	plan.CleanEnvironment = types.BoolValue(created.CleanEnvironment)
	plan.AdditionalOptions = types.StringValue(created.AdditionalOptions)
	plan.DevFSRuleset = types.StringValue(created.DevFSRuleset)
	plan.Fstab = types.StringValue(created.Fstab)
	plan.ResolvConf = types.StringValue(created.ResolvConf)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *jailResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state jailResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	jail, err := r.client.GetJail(ctx, int(state.CTID.ValueInt64()))
	if err != nil {
		if sylveclient.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Sylve jail", err.Error())
		return
	}

	state.DBID = types.Int64Value(int64(jail.DBID))
	state.Name = types.StringValue(jail.Name)
	state.Hostname = types.StringValue(jail.Hostname)
	state.Description = types.StringValue(jail.Description)
	state.Type = types.StringValue(jail.Type)
	state.Cores = types.Int64Value(int64(jail.Cores))
	state.Memory = types.Int64Value(int64(jail.Memory))
	state.ResourceLimits = types.BoolValue(jail.ResourceLimits)
	state.StartAtBoot = types.BoolValue(jail.StartAtBoot)
	state.StartOrder = types.Int64Value(int64(jail.StartOrder))
	state.CleanEnvironment = types.BoolValue(jail.CleanEnvironment)
	state.AdditionalOptions = types.StringValue(jail.AdditionalOptions)
	state.DevFSRuleset = types.StringValue(jail.DevFSRuleset)
	state.Fstab = types.StringValue(jail.Fstab)
	state.ResolvConf = types.StringValue(jail.ResolvConf)
	// pool/base/switch_name deliberately left untouched -- see the
	// schema's own descriptions.

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *jailResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state jailResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctid := int(state.CTID.ValueInt64())
	dbID := int(state.DBID.ValueInt64())

	if plan.Name.ValueString() != state.Name.ValueString() {
		if err := r.client.SetJailName(ctx, dbID, plan.Name.ValueString()); err != nil {
			resp.Diagnostics.AddError("Error updating Sylve jail name", err.Error())
			return
		}
	}
	if plan.Description.ValueString() != state.Description.ValueString() {
		if err := r.client.SetJailDescription(ctx, dbID, plan.Description.ValueString()); err != nil {
			resp.Diagnostics.AddError("Error updating Sylve jail description", err.Error())
			return
		}
	}
	if plan.Cores.ValueInt64() != state.Cores.ValueInt64() {
		if err := r.client.SetJailCPU(ctx, ctid, int(plan.Cores.ValueInt64())); err != nil {
			resp.Diagnostics.AddError("Error updating Sylve jail CPU", err.Error())
			return
		}
	}
	if plan.Memory.ValueInt64() != state.Memory.ValueInt64() {
		if err := r.client.SetJailMemory(ctx, ctid, int(plan.Memory.ValueInt64())); err != nil {
			resp.Diagnostics.AddError("Error updating Sylve jail memory", err.Error())
			return
		}
	}

	plan.ID = state.ID
	plan.DBID = state.DBID
	plan.CTID = state.CTID
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *jailResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state jailResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.DeleteJail(ctx, int(state.CTID.ValueInt64()))
	if err != nil && !sylveclient.IsNotFound(err) {
		resp.Diagnostics.AddError("Error deleting Sylve jail", err.Error())
	}
}

// ImportState accepts a bare CTID. pool/base/switch_name come back null
// on an imported resource (see the schema's own comment) -- omit them
// from config entirely for a clean plan, or accept a one-time forced
// replacement if you do declare them.
func (r *jailResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	ctid, err := strconv.ParseInt(req.ID, 10, 64)
	if err != nil {
		resp.Diagnostics.AddError(
			"Invalid import ID",
			fmt.Sprintf("Expected a numeric Sylve jail CTID (e.g. \"101\"), got %q: %s", req.ID, err),
		)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("ctid"), ctid)...)
}
