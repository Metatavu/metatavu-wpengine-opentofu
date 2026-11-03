package provider

import (
	"context"
	"sort"

	"github.com/Metatavu/metatavu-wpengine-opentofu/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &cacheExclusionsResource{}
	_ resource.ResourceWithConfigure   = &cacheExclusionsResource{}
	_ resource.ResourceWithImportState = &cacheExclusionsResource{}
)

// NewCacheExclusionsResource is wpengine_cache_exclusions.
func NewCacheExclusionsResource() resource.Resource {
	return &cacheExclusionsResource{}
}

type cacheExclusionsResource struct {
	client *client.Client
}

type cacheExclusionsModel struct {
	ID         types.String `tfsdk:"id"`
	InstallID  types.String `tfsdk:"install_id"`
	Exclusions types.Set    `tfsdk:"exclusions"`
}

type cacheExclusionModel struct {
	Type  types.String `tfsdk:"type"`
	Value types.String `tfsdk:"value"`
}

var cacheExclusionType = types.ObjectType{AttrTypes: map[string]attr.Type{"type": types.StringType, "value": types.StringType}}

func (r *cacheExclusionsResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_cache_exclusions"
}

func (r *cacheExclusionsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Every cache exclusion of an install, as one list: requests matching one bypass WP Engine's page cache. " +
			"Cookie exclusions are WP Engine's own: they can't be declared and aren't compared. " +
			"Destroying it removes the declared exclusions. Import by the install's ID or name.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"install_id": schema.StringAttribute{
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"exclusions": schema.SetNestedAttribute{
				Required: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"type": schema.StringAttribute{
							Required:            true,
							MarkdownDescription: "`url path` or `query arg`.",
							Validators:          []validator.String{stringvalidator.OneOf("url path", "query arg")},
						},
						"value": schema.StringAttribute{
							Required:            true,
							MarkdownDescription: "A regular expression the request's path or query argument is matched against.",
						},
					},
				},
			},
		},
	}
}

func (r *cacheExclusionsResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = configuredPlatform(req.ProviderData, &resp.Diagnostics)
}

func (r *cacheExclusionsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.write(ctx, req.Plan.Get, &resp.State, &resp.Diagnostics)
}

func (r *cacheExclusionsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.write(ctx, req.Plan.Get, &resp.State, &resp.Diagnostics)
}

func (r *cacheExclusionsResource) write(ctx context.Context, get func(context.Context, any) diag.Diagnostics, state interface {
	Set(context.Context, any) diag.Diagnostics
}, diags *diag.Diagnostics) {
	if !requirePlatform(r.client, diags) {
		return
	}
	var plan cacheExclusionsModel
	diags.Append(get(ctx, &plan)...)
	exclusions := exclusionList(ctx, plan.Exclusions, diags)
	if diags.HasError() {
		return
	}
	installID := plan.InstallID.ValueString()
	if err := r.client.SetCacheExclusions(ctx, installID, exclusions); err != nil {
		diags.AddError("Setting the cache exclusions of "+installID+" failed", err.Error())
		return
	}
	read, err := r.read(ctx, installID)
	if err != nil {
		diags.AddError("Reading the cache exclusions of "+installID+" failed", err.Error())
		return
	}
	diags.Append(state.Set(ctx, read)...)
}

func (r *cacheExclusionsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if !requirePlatform(r.client, &resp.Diagnostics) {
		return
	}
	var state cacheExclusionsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	read, err := r.read(ctx, state.InstallID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading the cache exclusions of "+state.InstallID.ValueString()+" failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, read)...)
}

func (r *cacheExclusionsResource) read(ctx context.Context, installID string) (cacheExclusionsModel, error) {
	exclusions, err := r.client.ListCacheExclusions(ctx, installID)
	if err != nil {
		return cacheExclusionsModel{}, err
	}
	elements := []attr.Value{}
	for _, exclusion := range exclusions {
		if exclusion.Type == client.CacheExclusionCookie {
			continue
		}
		object, _ := types.ObjectValue(cacheExclusionType.AttrTypes, map[string]attr.Value{
			"type": types.StringValue(exclusion.Type), "value": types.StringValue(exclusion.Value),
		})
		elements = append(elements, object)
	}
	set, _ := types.SetValue(cacheExclusionType, elements)
	return cacheExclusionsModel{ID: types.StringValue(installID), InstallID: types.StringValue(installID), Exclusions: set}, nil
}

func (r *cacheExclusionsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if !requirePlatform(r.client, &resp.Diagnostics) {
		return
	}
	var state cacheExclusionsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.SetCacheExclusions(ctx, state.InstallID.ValueString(), []client.CacheExclusion{}); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Removing the cache exclusions of "+state.InstallID.ValueString()+" failed", err.Error())
	}
}

// ImportState takes the install's ID or name.
func (r *cacheExclusionsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !requirePlatform(r.client, &resp.Diagnostics) {
		return
	}
	installID, err := resolveInstall(ctx, r.client, req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Finding install "+req.ID+" failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), installID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("install_id"), installID)...)
}

func exclusionList(ctx context.Context, set types.Set, diags *diag.Diagnostics) []client.CacheExclusion {
	var models []cacheExclusionModel
	diags.Append(set.ElementsAs(ctx, &models, false)...)
	out := make([]client.CacheExclusion, 0, len(models))
	for _, m := range models {
		out = append(out, client.CacheExclusion{Type: m.Type.ValueString(), Value: m.Value.ValueString()})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].Value < out[j].Value
	})
	return out
}
