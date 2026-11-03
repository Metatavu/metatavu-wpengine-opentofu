// Package provider is the wpengine OpenTofu provider.
package provider

import (
	"context"
	"os"

	"github.com/Metatavu/metatavu-wpengine-opentofu/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Provider configures the clients the resources share.
type Provider struct {
	version string
}

// New returns a constructor for the provider at this version.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &Provider{version: version}
	}
}

type providerModel struct {
	APIURL      types.String `tfsdk:"api_url"`
	APIUsername types.String `tfsdk:"api_username"`
	APIPassword types.String `tfsdk:"api_password"`
}

func (p *Provider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "wpengine"
	resp.Version = p.version
}

func (p *Provider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Declares what is Metatavu's concern about a WordPress project on WP Engine. " +
			"The `wpengine_*` platform resources use the WP Engine API, and need its credentials; nothing else does. " +
			"Each setting can come from its environment variable instead.",
		Attributes: map[string]schema.Attribute{
			"api_url": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "The WP Engine API, for tests. Defaults to `WPENGINE_API_URL`, then `" + client.DefaultURL + "`.",
			},
			"api_username": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "The API user's name, from the portal's API Access page (`WPENGINE_API_USERNAME`).",
			},
			"api_password": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				MarkdownDescription: "The API user's password (`WPENGINE_API_PASSWORD`).",
			},
		},
	}
}

func (p *Provider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if config.APIURL.IsUnknown() || config.APIUsername.IsUnknown() || config.APIPassword.IsUnknown() {
		resp.Diagnostics.AddError("Unknown provider configuration", "The wpengine provider's settings must be known when planning.")
		return
	}
	url := first(config.APIURL.ValueString(), os.Getenv("WPENGINE_API_URL"), client.DefaultURL)
	username := first(config.APIUsername.ValueString(), os.Getenv("WPENGINE_API_USERNAME"))
	password := first(config.APIPassword.ValueString(), os.Getenv("WPENGINE_API_PASSWORD"))

	data := &providerData{}
	switch {
	case username == "" && password == "":
		// site/ runs without WP Engine credentials (D3); platform resources say so when used.
	case username == "" || password == "":
		resp.Diagnostics.AddError("Incomplete WP Engine API credentials", "Give both api_username and api_password, or neither.")
		return
	default:
		c, err := client.New(url, username, password, nil)
		if err != nil {
			resp.Diagnostics.AddError("Can't use the WP Engine API", err.Error())
			return
		}
		data.platform = c
	}
	resp.ResourceData = data
	resp.DataSourceData = data
}

func (p *Provider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewSiteResource,
		NewInstallResource,
	}
}

func (p *Provider) DataSources(_ context.Context) []func() datasource.DataSource {
	return nil
}

func first(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
