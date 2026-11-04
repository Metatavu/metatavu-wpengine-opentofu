package provider

import (
	"context"

	"github.com/Metatavu/metatavu-wpengine-opentofu/internal/wordpress"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &wordpressActiveThemeResource{}
	_ resource.ResourceWithConfigure   = &wordpressActiveThemeResource{}
	_ resource.ResourceWithImportState = &wordpressActiveThemeResource{}
)

// NewWordPressActiveThemeResource is wpengine_wordpress_active_theme.
func NewWordPressActiveThemeResource() resource.Resource {
	return &wordpressActiveThemeResource{}
}

type wordpressActiveThemeResource struct {
	client *wordpress.Client
}

type wordpressActiveThemeModel struct {
	ID      types.String `tfsdk:"id"`
	Install types.String `tfsdk:"install"`
	Slug    types.String `tfsdk:"slug"`
}

func (r *wordpressActiveThemeResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_wordpress_active_theme"
}

func (r *wordpressActiveThemeResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The active theme of an install, through the IaC mu-plugin: a whitelisted, installed theme. " +
			"Removing the resource leaves the theme active. Import by the install's name.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"install": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The install's name.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"slug": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The theme's stylesheet (directory) name.",
			},
		},
	}
}

func (r *wordpressActiveThemeResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = configuredWordPress(req.ProviderData, &resp.Diagnostics)
}

func (r *wordpressActiveThemeResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan wordpressActiveThemeModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || !r.activate(ctx, plan, &resp.Diagnostics) {
		return
	}
	plan.ID = plan.Install
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *wordpressActiveThemeResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan wordpressActiveThemeModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || !r.activate(ctx, plan, &resp.Diagnostics) {
		return
	}
	plan.ID = plan.Install
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *wordpressActiveThemeResource) activate(ctx context.Context, plan wordpressActiveThemeModel, diags *diag.Diagnostics) bool {
	if err := r.client.PutActiveTheme(ctx, plan.Install.ValueString(), plan.Slug.ValueString()); err != nil {
		addWordPressError(diags, "Activating theme "+plan.Slug.ValueString()+" on "+plan.Install.ValueString()+" failed", err)
		return false
	}
	return true
}

func (r *wordpressActiveThemeResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state wordpressActiveThemeModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	live, err := r.client.State(ctx, state.Install.ValueString())
	if err != nil {
		addWordPressError(&resp.Diagnostics, "Reading "+state.Install.ValueString()+" failed", err)
		return
	}
	state.ID = state.Install
	state.Slug = types.StringValue(live.ActiveTheme)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

// Delete leaves the theme active: a site always has one.
func (r *wordpressActiveThemeResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

// ImportState takes the install's name.
func (r *wordpressActiveThemeResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("install"), req.ID)...)
}
