// Package provider implements the terraform-provider-sylve Terraform
// provider: https://github.com/ivomarino/terraform-provider-sylve
package provider

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ivomarino/terraform-provider-sylve/internal/sylveclient"
)

// Ensure SylveProvider satisfies the expected interfaces.
var _ provider.Provider = &SylveProvider{}

// SylveProvider is the terraform-provider-sylve provider implementation.
type SylveProvider struct {
	// version is set by the goreleaser build; "dev" for local builds.
	version string
}

// New returns a provider.Provider constructor for use with
// providerserver.NewProtocol6, parameterized by build version.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &SylveProvider{version: version}
	}
}

// sylveProviderModel is the provider's own configuration block.
type sylveProviderModel struct {
	Endpoint    types.String `tfsdk:"endpoint"`
	Username    types.String `tfsdk:"username"`
	Password    types.String `tfsdk:"password"`
	AuthType    types.String `tfsdk:"auth_type"`
	InsecureTLS types.Bool   `tfsdk:"insecure_tls"`
}

func (p *SylveProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "sylve"
	resp.Version = p.version
}

func (p *SylveProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Interacts with a Sylve node (https://sylve.io/) to manage bhyve VMs, jails, and related resources.",
		Attributes: map[string]schema.Attribute{
			"endpoint": schema.StringAttribute{
				Optional:    true,
				Description: "Sylve API base URL, e.g. \"https://sylve.example.com:8181\". Also read from SYLVE_ENDPOINT.",
			},
			"username": schema.StringAttribute{
				Optional:    true,
				Description: "Sylve (or PAM) username. Also read from SYLVE_USERNAME.",
			},
			"password": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "Sylve (or PAM) password. Also read from SYLVE_PASSWORD -- never hardcode this in committed configuration.",
			},
			"auth_type": schema.StringAttribute{
				Optional:    true,
				Description: "\"sylve\" (local Sylve account, the default) or \"pam\" (system account on the Sylve host). Also read from SYLVE_AUTH_TYPE.",
			},
			"insecure_tls": schema.BoolAttribute{
				Optional:    true,
				Description: "Skip TLS certificate verification. Only for self-signed test/dev Sylve instances. Also read from SYLVE_INSECURE_TLS (\"true\"/\"false\").",
			},
		},
	}
}

func (p *SylveProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config sylveProviderModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := firstNonEmpty(config.Endpoint.ValueString(), os.Getenv("SYLVE_ENDPOINT"))
	username := firstNonEmpty(config.Username.ValueString(), os.Getenv("SYLVE_USERNAME"))
	password := firstNonEmpty(config.Password.ValueString(), os.Getenv("SYLVE_PASSWORD"))
	authType := firstNonEmpty(config.AuthType.ValueString(), os.Getenv("SYLVE_AUTH_TYPE"), "sylve")
	insecureTLS := config.InsecureTLS.ValueBool() || os.Getenv("SYLVE_INSECURE_TLS") == "true"

	if endpoint == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("endpoint"),
			"Missing Sylve API endpoint",
			"Set the endpoint attribute or the SYLVE_ENDPOINT environment variable.",
		)
	}
	if username == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("username"),
			"Missing Sylve username",
			"Set the username attribute or the SYLVE_USERNAME environment variable.",
		)
	}
	if password == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("password"),
			"Missing Sylve password",
			"Set the password attribute or the SYLVE_PASSWORD environment variable -- never hardcode it in configuration.",
		)
	}
	if resp.Diagnostics.HasError() {
		return
	}

	client := sylveclient.NewClient(endpoint, username, password, authType, insecureTLS)
	if err := client.Login(ctx); err != nil {
		resp.Diagnostics.AddError("Unable to authenticate to Sylve", err.Error())
		return
	}

	resp.DataSourceData = client
	resp.ResourceData = client
}

func (p *SylveProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewVMResource,
		NewManualSwitchResource,
		NewZFSFilesystemResource,
		NewDownloadResource,
		NewJailResource,
		NewNetworkObjectResource,
		NewZFSVolumeResource,
		NewVMStorageResource,
		NewVMSnapshotResource,
		NewVMSnapshotRollbackResource,
		NewVMNetworkResource,
		NewVMPowerResource,
		NewJailSnapshotResource,
		NewJailSnapshotRollbackResource,
	}
}

func (p *SylveProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewVMGuestAgentDataSource,
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
