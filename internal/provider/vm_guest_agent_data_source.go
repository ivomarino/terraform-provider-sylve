package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ivomarino/terraform-provider-sylve/internal/sylveclient"
)

var (
	_ datasource.DataSource              = &vmGuestAgentDataSource{}
	_ datasource.DataSourceWithConfigure = &vmGuestAgentDataSource{}
)

// NewVMGuestAgentDataSource is the constructor registered in provider.DataSources.
// This is the first data source in this provider -- every other read
// surfaces through a resource's own Read (state Sylve manages); this one
// surfaces live guest-OS telemetry that Sylve itself doesn't manage or
// store, which is exactly the shape a data source is for.
func NewVMGuestAgentDataSource() datasource.DataSource {
	return &vmGuestAgentDataSource{}
}

type vmGuestAgentDataSource struct {
	client *sylveclient.Client
}

type vmGuestAgentDataSourceModel struct {
	RID             types.Int64  `tfsdk:"rid"`
	ID              types.String `tfsdk:"id"`
	OSName          types.String `tfsdk:"os_name"`
	OSKernelRelease types.String `tfsdk:"os_kernel_release"`
	OSVersion       types.String `tfsdk:"os_version"`
	OSPrettyName    types.String `tfsdk:"os_pretty_name"`
	OSVersionID     types.String `tfsdk:"os_version_id"`
	OSKernelVersion types.String `tfsdk:"os_kernel_version"`
	OSMachine       types.String `tfsdk:"os_machine"`
	OSID            types.String `tfsdk:"os_id"`
	Interfaces      types.List   `tfsdk:"interfaces"`
}

func (d *vmGuestAgentDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vm_guest_agent"
}

func (d *vmGuestAgentDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Live guest OS info and network interfaces, reported by the QEMU guest agent " +
			"channel inside a running sylve_vm. This is a data source, not a resource, because it's " +
			"read-only telemetry Sylve itself doesn't persist or manage -- see sylve_vm's qemu_guest_agent " +
			"attribute to enable the channel in the first place. Requires: qemu_guest_agent enabled on " +
			"the VM, the guest-agent package actually installed and running inside the guest (nothing " +
			"installs it automatically -- see sylve_vm's cloud_init attributes for one way to get it " +
			"there), and the VM running. Errors with a timeout if the agent isn't there to answer.",
		Attributes: map[string]schema.Attribute{
			"rid": schema.Int64Attribute{
				Required:    true,
				Description: "RID of the sylve_vm to query.",
			},
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Terraform resource identity -- the VM's RID as a string.",
			},
			"os_name": schema.StringAttribute{
				Computed: true,
			},
			"os_kernel_release": schema.StringAttribute{
				Computed: true,
			},
			"os_version": schema.StringAttribute{
				Computed: true,
			},
			"os_pretty_name": schema.StringAttribute{
				Computed: true,
			},
			"os_version_id": schema.StringAttribute{
				Computed: true,
			},
			"os_kernel_version": schema.StringAttribute{
				Computed: true,
			},
			"os_machine": schema.StringAttribute{
				Computed: true,
			},
			"os_id": schema.StringAttribute{
				Computed: true,
			},
			"interfaces": schema.ListNestedAttribute{
				Computed:    true,
				Description: "Every network interface the guest OS itself reports, including loopback.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Computed: true,
						},
						"hardware_address": schema.StringAttribute{
							Computed: true,
						},
						"ip_addresses": schema.ListNestedAttribute{
							Computed: true,
							NestedObject: schema.NestedAttributeObject{
								Attributes: map[string]schema.Attribute{
									"type": schema.StringAttribute{
										Computed:    true,
										Description: "\"ipv4\" or \"ipv6\".",
									},
									"address": schema.StringAttribute{
										Computed: true,
									},
									"prefix": schema.Int64Attribute{
										Computed: true,
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func (d *vmGuestAgentDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*sylveclient.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected data source configure type",
			fmt.Sprintf("Expected *sylveclient.Client, got: %T. Report this issue to the provider maintainers.", req.ProviderData),
		)
		return
	}
	d.client = client
}

func (d *vmGuestAgentDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config vmGuestAgentDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	rid := int(config.RID.ValueInt64())
	info, err := d.client.GetQGAInfo(ctx, rid)
	if err != nil {
		resp.Diagnostics.AddError("Error querying Sylve QEMU guest agent", err.Error())
		return
	}

	config.ID = types.StringValue(fmt.Sprintf("%d", rid))
	config.OSName = types.StringValue(info.OSInfo.Name)
	config.OSKernelRelease = types.StringValue(info.OSInfo.KernelRelease)
	config.OSVersion = types.StringValue(info.OSInfo.Version)
	config.OSPrettyName = types.StringValue(info.OSInfo.PrettyName)
	config.OSVersionID = types.StringValue(info.OSInfo.VersionID)
	config.OSKernelVersion = types.StringValue(info.OSInfo.KernelVersion)
	config.OSMachine = types.StringValue(info.OSInfo.Machine)
	config.OSID = types.StringValue(info.OSInfo.ID)

	ipAddrType := types.ObjectType{AttrTypes: map[string]attr.Type{
		"type":    types.StringType,
		"address": types.StringType,
		"prefix":  types.Int64Type,
	}}
	ifaceType := types.ObjectType{AttrTypes: map[string]attr.Type{
		"name":             types.StringType,
		"hardware_address": types.StringType,
		"ip_addresses":     types.ListType{ElemType: ipAddrType},
	}}

	ifaceValues := make([]attr.Value, 0, len(info.Interfaces))
	for _, iface := range info.Interfaces {
		ipValues := make([]attr.Value, 0, len(iface.IPAddresses))
		for _, ip := range iface.IPAddresses {
			ipObj, diags := types.ObjectValue(ipAddrType.AttrTypes, map[string]attr.Value{
				"type":    types.StringValue(ip.Type),
				"address": types.StringValue(ip.Address),
				"prefix":  types.Int64Value(int64(ip.Prefix)),
			})
			resp.Diagnostics.Append(diags...)
			ipValues = append(ipValues, ipObj)
		}
		ipList, diags := types.ListValue(ipAddrType, ipValues)
		resp.Diagnostics.Append(diags...)

		ifaceObj, diags := types.ObjectValue(ifaceType.AttrTypes, map[string]attr.Value{
			"name":             types.StringValue(iface.Name),
			"hardware_address": types.StringValue(iface.HardwareAddress),
			"ip_addresses":     ipList,
		})
		resp.Diagnostics.Append(diags...)
		ifaceValues = append(ifaceValues, ifaceObj)
	}
	if resp.Diagnostics.HasError() {
		return
	}

	ifaceList, diags := types.ListValue(ifaceType, ifaceValues)
	resp.Diagnostics.Append(diags...)
	config.Interfaces = ifaceList

	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
