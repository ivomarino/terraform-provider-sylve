package provider

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ivomarino/terraform-provider-sylve/internal/sylveclient"
)

// vmPowerWaitTimeout bounds how long this resource polls for a
// start/stop to actually take effect. A VM waiting for a VNC connection
// would never reach "Running" at all before this fix existed (see
// sylve_vm's vnc_wait) -- this timeout is what would have surfaced that
// bug as a clear error instead of Terraform believing a create/apply
// succeeded while the VM sat frozen.
const vmPowerWaitTimeout = 2 * time.Minute

var (
	_ resource.Resource                = &vmPowerResource{}
	_ resource.ResourceWithConfigure   = &vmPowerResource{}
	_ resource.ResourceWithImportState = &vmPowerResource{}
)

// NewVMPowerResource is the constructor registered in provider.Resources.
func NewVMPowerResource() resource.Resource {
	return &vmPowerResource{}
}

type vmPowerResource struct {
	client *sylveclient.Client
}

// vmPowerResourceModel: unlike sylve_vm_snapshot_rollback, this genuinely
// is ordinary managed state, not a one-shot action -- "is this VM
// running" is a real, idempotent, externally-verifiable property (via
// GetDomainStatus), so Read() actually reflects live drift (someone
// starting/stopping the VM outside Terraform shows up as a plan diff,
// same as any other attribute).
type vmPowerResourceModel struct {
	ID    types.String `tfsdk:"id"` // string form of rid
	RID   types.Int64  `tfsdk:"rid"`
	State types.String `tfsdk:"state"`
}

func (r *vmPowerResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vm_power"
}

func (r *vmPowerResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages whether a sylve_vm is running or stopped. This is real, drift-detected " +
			"state (via the live libvirt domain status), not a one-shot action like " +
			"sylve_vm_snapshot_rollback -- starting/stopping the VM outside Terraform shows up as a plan " +
			"diff. Separate from sylve_vm itself so that creating a VM doesn't implicitly start it (or " +
			"vice versa), and so a VM's boot state can be toggled without touching its own configuration.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Terraform resource identity -- the VM's RID as a string.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"rid": schema.Int64Attribute{
				Required:      true,
				Description:   "RID of the sylve_vm to manage the power state of. Immutable.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"state": schema.StringAttribute{
				Required: true,
				Description: "\"running\" or \"stopped\". Not validated client-side; an invalid value is " +
					"rejected server-side. Applying blocks (up to 2 minutes) until the VM actually " +
					"reaches the requested domain status -- catches a VM that's stuck (e.g. paused " +
					"waiting for a VNC connection, see sylve_vm's vnc_wait) as a clear error instead of " +
					"Terraform silently believing the change succeeded.",
			},
		},
	}
}

func (r *vmPowerResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// domainStatusFor maps this resource's "running"/"stopped" vocabulary to
// Sylve's own domain status strings.
func domainStatusFor(state string) string {
	if state == "running" {
		return "Running"
	}
	return "Shutoff"
}

func (r *vmPowerResource) applyState(ctx context.Context, rid int, state string) error {
	action := sylveclient.VMActionStop
	if state == "running" {
		action = sylveclient.VMActionStart
	}
	if err := r.client.DoVMAction(ctx, rid, action); err != nil {
		return fmt.Errorf("queueing VM action %q: %w", action, err)
	}
	return r.client.WaitForDomainStatus(ctx, rid, domainStatusFor(state), vmPowerWaitTimeout)
}

func (r *vmPowerResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan vmPowerResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	rid := int(plan.RID.ValueInt64())
	if err := r.applyState(ctx, rid, plan.State.ValueString()); err != nil {
		resp.Diagnostics.AddError("Error setting Sylve VM power state", err.Error())
		return
	}

	plan.ID = types.StringValue(fmt.Sprintf("%d", rid))
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *vmPowerResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state vmPowerResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	status, err := r.client.GetDomainStatus(ctx, int(state.RID.ValueInt64()))
	if err != nil {
		if sylveclient.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Sylve VM power state", err.Error())
		return
	}

	if status == "Running" {
		state.State = types.StringValue("running")
	} else {
		state.State = types.StringValue("stopped")
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *vmPowerResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state vmPowerResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if plan.State.ValueString() != state.State.ValueString() {
		if err := r.applyState(ctx, int(state.RID.ValueInt64()), plan.State.ValueString()); err != nil {
			resp.Diagnostics.AddError("Error setting Sylve VM power state", err.Error())
			return
		}
	}

	plan.ID = state.ID
	plan.RID = state.RID
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete deliberately does nothing to the VM's actual power state --
// removing this resource from Terraform just stops managing it, it
// doesn't imply "stop the VM" (that would be a surprising, destructive
// side effect of an otherwise routine `terraform destroy` /
// config-removal).
func (r *vmPowerResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
}

// ImportState accepts a bare RID.
func (r *vmPowerResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	rid, err := strconv.ParseInt(req.ID, 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("Expected a numeric Sylve VM RID, got %q: %s", req.ID, err))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("rid"), rid)...)
}
