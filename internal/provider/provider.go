// Package provider is the wpengine OpenTofu provider.
package provider

import (
	"context"
	"os"

	"github.com/Metatavu/metatavu-wpengine-opentofu/internal/client"
	"github.com/Metatavu/metatavu-wpengine-opentofu/internal/wordpress"
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

	WordPressEndpoints    types.Map    `tfsdk:"wordpress_endpoints"`
	WordPressTokenCommand types.String `tfsdk:"wordpress_token_command"`
	ReleaseToken          types.String `tfsdk:"release_token"`
}

func (p *Provider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "wpengine"
	resp.Version = p.version
}

func (p *Provider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Declares what is Metatavu's concern about a WordPress project on WP Engine. " +
			"The `wpengine_*` platform resources use the WP Engine API, and need its credentials; nothing else does. " +
			"The `wpengine_wordpress_*` resources use the metatavu-wordpress-iac mu-plugin on each install, with GitHub Actions OIDC tokens: " +
			"they work only in a workflow with `permissions: id-token: write`. " +
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
			"wordpress_endpoints": schema.MapAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Install name to base URL, for tests, overriding `https://<install>.wpengine.com`.",
			},
			"wordpress_token_command": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "A command that prints a token for the install in `IAC_AUDIENCE`, run for each token, for tests (`WPENGINE_WORDPRESS_TOKEN_COMMAND`). Without it, tokens come from GitHub Actions.",
			},
			"release_token": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				MarkdownDescription: "A token for downloading release zips from private GitHub releases (`WPENGINE_RELEASE_TOKEN`).",
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
	if config.APIURL.IsUnknown() || config.APIUsername.IsUnknown() || config.APIPassword.IsUnknown() ||
		config.WordPressEndpoints.IsUnknown() || config.WordPressTokenCommand.IsUnknown() || config.ReleaseToken.IsUnknown() {
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

	endpoints := map[string]string{}
	resp.Diagnostics.Append(config.WordPressEndpoints.ElementsAs(ctx, &endpoints, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var tokens wordpress.TokenSource
	if command := first(config.WordPressTokenCommand.ValueString(), os.Getenv("WPENGINE_WORDPRESS_TOKEN_COMMAND")); command != "" {
		tokens = wordpress.CommandTokens(command)
	} else if actions := wordpress.ActionsTokens(nil); actions != nil {
		tokens = actions
	}
	data.wordpress = wordpress.New(endpoints, tokens, nil)
	data.releaseToken = first(config.ReleaseToken.ValueString(), os.Getenv("WPENGINE_RELEASE_TOKEN"))

	resp.ResourceData = data
	resp.DataSourceData = data
}

func (p *Provider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewSiteResource,
		NewInstallResource,
		NewDomainResource,
		NewCacheExclusionsResource,
		NewAccountUserResource,
		NewSSHKeyResource,
		NewWordPressOptionResource,
		NewWordPressActiveThemeResource,
		NewWordPressPluginResource,
		NewWordPressThemeResource,
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
