package provider

import (
	"context"
	"regexp"

	"github.com/Metatavu/metatavu-wpengine-opentofu/internal/wordpress"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                   = &wordpressPackageResource{}
	_ resource.ResourceWithConfigure      = &wordpressPackageResource{}
	_ resource.ResourceWithImportState    = &wordpressPackageResource{}
	_ resource.ResourceWithValidateConfig = &wordpressPackageResource{}
)

// NewWordPressPluginResource is wpengine_wordpress_plugin.
func NewWordPressPluginResource() resource.Resource {
	return &wordpressPackageResource{kind: wordpress.Plugins}
}

// NewWordPressThemeResource is wpengine_wordpress_theme.
func NewWordPressThemeResource() resource.Resource {
	return &wordpressPackageResource{kind: wordpress.Themes}
}

// wordpressPackageResource is a whitelisted plugin or theme.
type wordpressPackageResource struct {
	kind         string
	client       *wordpress.Client
	releaseToken string
}

type wordpressPackageModel struct {
	ID               types.String `tfsdk:"id"`
	Install          types.String `tfsdk:"install"`
	Slug             types.String `tfsdk:"slug"`
	Source           types.String `tfsdk:"source"`
	URL              types.String `tfsdk:"url"`
	Version          types.String `tfsdk:"version"`
	SHA256           types.String `tfsdk:"sha256"`
	Active           types.Bool   `tfsdk:"active"`
	InstalledVersion types.String `tfsdk:"installed_version"`
}

const (
	sourceWordPressOrg = "wordpress.org"
	sourceRelease      = "release"
)

func (r *wordpressPackageResource) noun() string {
	if r.kind == wordpress.Plugins {
		return "plugin"
	}
	return "theme"
}

func (r *wordpressPackageResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_wordpress_" + r.noun()
}

func (r *wordpressPackageResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	attributes := map[string]schema.Attribute{
		"id": schema.StringAttribute{
			Computed:      true,
			PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
		"install": schema.StringAttribute{Required: true, MarkdownDescription: "The install's name.", PlanModifiers: replace},
		"slug":    schema.StringAttribute{Required: true, MarkdownDescription: "The " + r.noun() + "'s directory name.", PlanModifiers: replace},
		"source": schema.StringAttribute{
			Required:            true,
			MarkdownDescription: "`wordpress.org`, installed by slug and kept up to date by WP Engine, or `release`, a zip at `url` with its `version` and `sha256`. Changing it replaces the " + r.noun() + ".",
			Validators:          []validator.String{stringvalidator.OneOf(sourceWordPressOrg, sourceRelease)},
			// An imported package has no source in state: it's adopted, not replaced.
			PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplaceIf(
				func(_ context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
					resp.RequiresReplace = !req.StateValue.IsNull()
				},
				"Changing the source replaces the package.", "Changing the source replaces the package.",
			)},
		},
		"url": schema.StringAttribute{
			Optional:            true,
			MarkdownDescription: "A release's zip. The provider downloads it in CI (with `release_token` for private GitHub releases), checks its `sha256`, and uploads it to the site.",
		},
		"version": schema.StringAttribute{
			Optional:            true,
			MarkdownDescription: "A release's version, as its header declares it.",
		},
		"sha256": schema.StringAttribute{
			Optional:            true,
			MarkdownDescription: "A release zip's SHA-256.",
			Validators:          []validator.String{stringvalidator.RegexMatches(regexp.MustCompile(`^[0-9a-f]{64}$`), "must be a lowercase hex SHA-256")},
		},
		"installed_version": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "The version installed, which WP Engine keeps up to date for wordpress.org packages.",
		},
	}
	description := "A whitelisted " + r.noun() + " on an install, through the IaC mu-plugin. Destroying it "
	if r.kind == wordpress.Plugins {
		attributes["active"] = schema.BoolAttribute{
			Optional:            true,
			Computed:            true,
			Default:             booldefault.StaticBool(true),
			MarkdownDescription: "Whether the plugin is active. Changing it for a release uploads the zip again.",
		}
		description += "deactivates and deletes the plugin."
	} else {
		attributes["active"] = schema.BoolAttribute{
			Computed:            true,
			MarkdownDescription: "Whether it's the active theme, which `wpengine_wordpress_active_theme` sets.",
		}
		description += "deletes the theme; the active theme can't be deleted."
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: description + " Import as `<install>/<slug>`: an imported package whose installed version is the declared one isn't reinstalled.",
		Attributes:          attributes,
	}
}

func (r *wordpressPackageResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config wordpressPackageModel
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("source"), &config.Source)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("url"), &config.URL)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("version"), &config.Version)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("sha256"), &config.SHA256)...)
	if resp.Diagnostics.HasError() || config.Source.IsUnknown() {
		return
	}
	release := config.Source.ValueString() == sourceRelease
	for name, value := range map[string]types.String{"url": config.URL, "version": config.Version, "sha256": config.SHA256} {
		switch {
		case release && value.IsNull():
			resp.Diagnostics.AddAttributeError(path.Root(name), "A release needs "+name, "Give url, version and sha256 for a release.")
		case !release && !value.IsNull():
			resp.Diagnostics.AddAttributeError(path.Root(name), "Only a release has "+name, "A wordpress.org package is installed by slug; WP Engine keeps it up to date.")
		}
	}
}

func (r *wordpressPackageResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = configuredWordPress(req.ProviderData, &resp.Diagnostics)
	if data, ok := req.ProviderData.(*providerData); ok {
		r.releaseToken = data.releaseToken
	}
}

func (r *wordpressPackageResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan wordpressPackageModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.install(ctx, &plan, &resp.Diagnostics)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	}
}

func (r *wordpressPackageResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state wordpressPackageModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// An imported release whose installed version is the declared one, with
	// the declared activation, is adopted without uploading it again.
	adopting := state.Source.IsNull() && plan.Source.ValueString() == sourceRelease &&
		state.InstalledVersion.Equal(plan.Version) && (r.kind == wordpress.Themes || state.Active.Equal(plan.Active))
	if adopting {
		plan.InstalledVersion = state.InstalledVersion
		if r.kind == wordpress.Themes {
			plan.Active = state.Active
		}
	} else {
		r.install(ctx, &plan, &resp.Diagnostics)
	}
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	}
}

// install installs, updates or (de)activates the package as planned.
func (r *wordpressPackageResource) install(ctx context.Context, plan *wordpressPackageModel, diags *diag.Diagnostics) {
	install, slug := plan.Install.ValueString(), plan.Slug.ValueString()
	active := r.kind == wordpress.Plugins && plan.Active.ValueBool()
	var (
		state *wordpress.PackageState
		err   error
	)
	if plan.Source.ValueString() == sourceWordPressOrg {
		state, err = r.client.InstallFromWordPressOrg(ctx, install, r.kind, slug, active)
	} else {
		zip, downloadErr := wordpress.DownloadRelease(ctx, r.client.HTTP(), plan.URL.ValueString(), plan.SHA256.ValueString(), r.releaseToken)
		if downloadErr != nil {
			diags.AddError("Downloading the release of "+slug+" failed", downloadErr.Error())
			return
		}
		state, err = r.client.UploadRelease(ctx, install, r.kind, slug, zip, plan.Version.ValueString(), plan.SHA256.ValueString(), active)
	}
	if err != nil {
		addWordPressError(diags, "Installing "+r.noun()+" "+slug+" on "+install+" failed", err)
		return
	}
	plan.ID = types.StringValue(install + "/" + slug)
	plan.InstalledVersion = stringValue(state.Version)
	if r.kind == wordpress.Themes {
		live, err := r.client.State(ctx, install)
		if err != nil {
			addWordPressError(diags, "Reading "+install+" failed", err)
			return
		}
		plan.Active = types.BoolValue(live.ActiveTheme == slug)
	}
}

func (r *wordpressPackageResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state wordpressPackageModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	install, slug := state.Install.ValueString(), state.Slug.ValueString()
	live, err := r.client.State(ctx, install)
	if err != nil {
		addWordPressError(&resp.Diagnostics, "Reading "+install+" failed", err)
		return
	}
	var (
		installed, active bool
		version           *string
		whitelisted       bool
	)
	if r.kind == wordpress.Plugins {
		var plugin wordpress.PluginState
		plugin, whitelisted = live.Plugins[slug]
		installed, version, active = plugin.Installed, plugin.Version, plugin.Active
	} else {
		var theme wordpress.ThemeState
		theme, whitelisted = live.Themes[slug]
		installed, version = theme.Installed, theme.Version
	}
	if !whitelisted {
		resp.Diagnostics.AddError(r.noun()+" "+slug+" isn't managed on "+install,
			"It isn't on the install's whitelist. Add it to the whitelist in the project's bootstrap/ and apply that first.")
		return
	}
	if !installed {
		resp.State.RemoveResource(ctx)
		return
	}
	state.ID = types.StringValue(install + "/" + slug)
	state.InstalledVersion = stringValue(version)
	// A release shows a version changed in wp-admin as drift.
	if !state.Version.IsNull() {
		state.Version = stringValue(version)
	}
	if r.kind == wordpress.Plugins {
		state.Active = types.BoolValue(active)
	} else {
		state.Active = types.BoolValue(live.ActiveTheme == slug)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *wordpressPackageResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state wordpressPackageModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeletePackage(ctx, state.Install.ValueString(), r.kind, state.Slug.ValueString()); err != nil {
		addWordPressError(&resp.Diagnostics, "Deleting "+r.noun()+" "+state.Slug.ValueString()+" failed", err)
	}
}

// ImportState takes "<install>/<slug>".
func (r *wordpressPackageResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	install, slug, ok := splitImportID(req.ID, "<install>/<slug>", &resp.Diagnostics)
	if !ok {
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("install"), install)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("slug"), slug)...)
}
