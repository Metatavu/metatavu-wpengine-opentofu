package provider

import (
	"context"
	"strings"

	"github.com/Metatavu/metatavu-wpengine-opentofu/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &siteResource{}
	_ resource.ResourceWithConfigure   = &siteResource{}
	_ resource.ResourceWithImportState = &siteResource{}
)

// NewSiteResource is wpengine_site.
func NewSiteResource() resource.Resource {
	return &siteResource{}
}

type siteResource struct {
	client *client.Client
}

type siteModel struct {
	ID        types.String `tfsdk:"id"`
	AccountID types.String `tfsdk:"account_id"`
	Name      types.String `tfsdk:"name"`
}

func (r *siteResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_site"
}

func (r *siteResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A WP Engine site: the group of a project's installs. " +
			"Destroying a site is refused while it still has installs, because WP Engine deletes them with it. " +
			"Import by ID or by name.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"account_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The WP Engine account the site belongs to.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The site's name in the portal.",
			},
		},
	}
}

func (r *siteResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = configuredPlatform(req.ProviderData, &resp.Diagnostics)
}

func (r *siteResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if !requirePlatform(r.client, &resp.Diagnostics) {
		return
	}
	var plan siteModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	site, err := r.client.CreateSite(ctx, client.SiteCreate{Name: plan.Name.ValueString(), AccountID: plan.AccountID.ValueString()})
	if err != nil {
		resp.Diagnostics.AddError("Creating site "+plan.Name.ValueString()+" failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, siteState(site))...)
}

func (r *siteResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if !requirePlatform(r.client, &resp.Diagnostics) {
		return
	}
	var state siteModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	site, err := r.client.GetSite(ctx, state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading site "+state.Name.ValueString()+" failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, siteState(site))...)
}

func (r *siteResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if !requirePlatform(r.client, &resp.Diagnostics) {
		return
	}
	var plan, state siteModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	site, err := r.client.UpdateSite(ctx, state.ID.ValueString(), client.SiteUpdate{Name: plan.Name.ValueString()})
	if err != nil {
		resp.Diagnostics.AddError("Renaming site "+state.Name.ValueString()+" failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, siteState(site))...)
}

func (r *siteResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if !requirePlatform(r.client, &resp.Diagnostics) {
		return
	}
	var state siteModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	name := state.Name.ValueString()
	site, err := r.client.GetSite(ctx, state.ID.ValueString())
	if client.IsNotFound(err) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading site "+name+" failed", err.Error())
		return
	}
	if len(site.Installs) > 0 {
		names := make([]string, len(site.Installs))
		for i, install := range site.Installs {
			names[i] = install.Name
		}
		resp.Diagnostics.AddError("Site "+name+" still has installs",
			"WP Engine deletes a site's installs with it. Destroy or move these installs first: "+strings.Join(names, ", "))
		return
	}
	if err := r.client.DeleteSite(ctx, state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting site "+name+" failed", err.Error())
	}
}

// ImportState takes a site's ID or its name.
func (r *siteResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if uuidPattern.MatchString(req.ID) {
		resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
		return
	}
	if !requirePlatform(r.client, &resp.Diagnostics) {
		return
	}
	sites, err := r.client.ListSites(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Listing sites failed", err.Error())
		return
	}
	var found []client.Site
	for _, site := range sites {
		if site.Name == req.ID {
			found = append(found, site)
		}
	}
	switch len(found) {
	case 0:
		resp.Diagnostics.AddError("No such site", "The API user sees no site named "+req.ID+".")
	case 1:
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), found[0].ID)...)
	default:
		resp.Diagnostics.AddError("Ambiguous site name", "More than one site is named "+req.ID+": import it by ID.")
	}
}

func siteState(site *client.Site) siteModel {
	return siteModel{
		ID:        types.StringValue(site.ID),
		AccountID: types.StringValue(site.Account.ID),
		Name:      types.StringValue(site.Name),
	}
}
