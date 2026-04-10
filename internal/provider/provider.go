// Package provider implements the VyOS Terraform provider using
// terraform-plugin-framework.
package provider

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/hiranadikari/terraform-provider-vyos/internal/clients"
	"github.com/hiranadikari/terraform-provider-vyos/internal/services/config"
)

var _ provider.Provider = &VyOSProvider{}

// VyOSProvider is the root provider implementation.
type VyOSProvider struct {
	version string
}

// New returns a factory function that creates a new provider instance.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &VyOSProvider{version: version}
	}
}

func (p *VyOSProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "vyos"
	resp.Version = p.version
}

func (p *VyOSProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The **VyOS** provider manages configuration on a VyOS router/gateway via the VyOS HTTPS REST API.",

		Attributes: map[string]schema.Attribute{
			"endpoint": schema.StringAttribute{
				MarkdownDescription: "VyOS HTTPS API endpoint (e.g. `https://192.168.0.1`). " +
					"Can also be set via the `VYOS_ENDPOINT` environment variable.",
				Optional: true,
			},
			"api_key": schema.StringAttribute{
				MarkdownDescription: "VyOS API key configured under `set service https api keys id <id> key <key>`. " +
					"Can also be set via the `VYOS_API_KEY` environment variable.",
				Optional:  true,
				Sensitive: true,
			},
			"insecure": schema.BoolAttribute{
				MarkdownDescription: "Disable TLS certificate verification. " +
					"Set to `true` when VyOS uses a self-signed certificate.",
				Optional: true,
			},
		},
	}
}

type providerModel struct {
	Endpoint types.String `tfsdk:"endpoint"`
	APIKey   types.String `tfsdk:"api_key"`
	Insecure types.Bool   `tfsdk:"insecure"`
}

func (p *VyOSProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := coalesce(cfg.Endpoint.ValueString(), os.Getenv("VYOS_ENDPOINT"))
	apiKey := coalesce(cfg.APIKey.ValueString(), os.Getenv("VYOS_API_KEY"))
	insecure := cfg.Insecure.ValueBool()

	if endpoint == "" {
		resp.Diagnostics.AddError(
			"Missing endpoint",
			"Set endpoint in the provider block or the VYOS_ENDPOINT environment variable.",
		)
	}
	if apiKey == "" {
		resp.Diagnostics.AddError(
			"Missing api_key",
			"Set api_key in the provider block or the VYOS_API_KEY environment variable.",
		)
	}
	if resp.Diagnostics.HasError() {
		return
	}

	client := clients.Build(endpoint, apiKey, insecure)
	resp.ResourceData = client
	resp.DataSourceData = client
}

func (p *VyOSProvider) Resources(_ context.Context) []func() resource.Resource {
	return config.Resources()
}

func (p *VyOSProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{}
}

func coalesce(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
