package provider

import (
	"context"

	"github.com/Metatavu/metatavu-wpengine-opentofu/internal/wordpress"
	"github.com/hashicorp/terraform-plugin-framework-validators/resourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                     = &wordpressOptionResource{}
	_ resource.ResourceWithConfigure        = &wordpressOptionResource{}
	_ resource.ResourceWithImportState      = &wordpressOptionResource{}
	_ resource.ResourceWithModifyPlan       = &wordpressOptionResource{}
	_ resource.ResourceWithConfigValidators = &wordpressOptionResource{}
)

// NewWordPressOptionResource is wpengine_wordpress_option.
func NewWordPressOptionResource() resource.Resource {
	return &wordpressOptionResource{}
}

type wordpressOptionResource struct {
	client *wordpress.Client
}

type wordpressOptionModel struct {
	ID             types.String `tfsdk:"id"`
	Install        types.String `tfsdk:"install"`
	Name           types.String `tfsdk:"name"`
	Value          optionJSON   `tfsdk:"value"`
	SensitiveValue optionJSON   `tfsdk:"sensitive_value"`
	SensitiveHash  types.String `tfsdk:"sensitive_value_sha256"`
}

func (r *wordpressOptionResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_wordpress_option"
}

func (r *wordpressOptionResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A WordPress option on an install, through the IaC mu-plugin; it must be on the install's whitelist. " +
			"Give exactly one of `value` and `sensitive_value`, as JSON (`jsonencode()`). Values are compared as WordPress stores them: " +
			"a top-level number or boolean is a string, and `{}` is `[]`. A `sensitive_value` is compared through its hash and never appears in a plan. " +
			"Removing the resource leaves the option's value in WordPress. Import as `<install>/<name>`: the first plan after importing a value that " +
			"isn't spelled as WordPress stores it (a number, a boolean, `{}`), or a `sensitive_value`, shows an update, and its apply writes nothing.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"install": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The install's name.",
				PlanModifiers:       replace,
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The option's name.",
				PlanModifiers:       replace,
			},
			"value": schema.StringAttribute{
				Optional:            true,
				CustomType:          optionJSONType{},
				MarkdownDescription: "The value, as JSON.",
			},
			"sensitive_value": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				CustomType:          optionJSONType{},
				MarkdownDescription: "A secret value, as JSON. The option must be whitelisted as sensitive, so the plugin returns only its hash.",
			},
			"sensitive_value_sha256": schema.StringAttribute{
				Computed:            true,
				Sensitive:           true,
				MarkdownDescription: "The hash a `sensitive_value` is compared through.",
			},
		},
	}
}

func (r *wordpressOptionResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		resourcevalidator.ExactlyOneOf(path.MatchRoot("value"), path.MatchRoot("sensitive_value")),
	}
}

func (r *wordpressOptionResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = configuredWordPress(req.ProviderData, &resp.Diagnostics)
}

// ModifyPlan works out the hash a sensitive value is compared through, so a
// change to the secret, or to the live value, shows as a change to the hash.
func (r *wordpressOptionResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var plan wordpressOptionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	switch {
	case plan.SensitiveValue.IsUnknown():
		plan.SensitiveHash = types.StringUnknown()
	case plan.SensitiveValue.IsNull():
		plan.SensitiveHash = types.StringNull()
	default:
		normalised, err := wordpress.Normalise(plan.SensitiveValue.ValueString())
		if err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("sensitive_value"), "Invalid JSON", err.Error())
			return
		}
		plan.SensitiveHash = types.StringValue(wordpress.SHA256(normalised))
	}
	if plan.Install.IsUnknown() || plan.Name.IsUnknown() {
		plan.ID = types.StringUnknown()
	} else {
		plan.ID = types.StringValue(plan.Install.ValueString() + "/" + plan.Name.ValueString())
	}
	resp.Diagnostics.Append(resp.Plan.Set(ctx, plan)...)
}

func (r *wordpressOptionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan wordpressOptionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.write(ctx, plan, &resp.Diagnostics)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	}
}

func (r *wordpressOptionResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state wordpressOptionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// After an import, the state holds WordPress's spelling of the value (or,
	// for a secret, only its hash), so the first plan shows an update even
	// when the values are the same once normalised: then nothing is written.
	adopting := !plan.SensitiveValue.IsNull() && plan.SensitiveHash.Equal(state.SensitiveHash) && state.SensitiveValue.IsNull()
	if !adopting && !(plan.Value.IsNull() || state.Value.IsNull()) && wordpress.Equal(plan.Value.ValueString(), state.Value.ValueString()) {
		adopting = true
	}
	if !adopting {
		r.write(ctx, plan, &resp.Diagnostics)
	}
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	}
}

func (r *wordpressOptionResource) write(ctx context.Context, plan wordpressOptionModel, diags *diag.Diagnostics) {
	raw := plan.Value.ValueString()
	if plan.Value.IsNull() {
		raw = plan.SensitiveValue.ValueString()
	}
	value, err := wordpress.Normalise(raw)
	if err != nil {
		diags.AddError("Invalid JSON", err.Error())
		return
	}
	// A secret goes only to an option whitelisted as sensitive: the plugin
	// returns any other option's value to every read token.
	if !plan.SensitiveValue.IsNull() {
		live, err := r.client.State(ctx, plan.Install.ValueString())
		if err != nil {
			addWordPressError(diags, "Reading "+plan.Install.ValueString()+" failed", err)
			return
		}
		if option, ok := live.Options[plan.Name.ValueString()]; ok && !option.Sensitive {
			diags.Append(notSensitive(plan.Install.ValueString(), plan.Name.ValueString()))
			return
		}
	}
	if _, err := r.client.PutOption(ctx, plan.Install.ValueString(), plan.Name.ValueString(), value); err != nil {
		addWordPressError(diags, "Setting option "+plan.Name.ValueString()+" on "+plan.Install.ValueString()+" failed", err)
	}
}

func (r *wordpressOptionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state wordpressOptionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	install, name := state.Install.ValueString(), state.Name.ValueString()
	live, err := r.client.State(ctx, install)
	if err != nil {
		addWordPressError(&resp.Diagnostics, "Reading "+install+" failed", err)
		return
	}
	option, whitelisted := live.Options[name]
	if !whitelisted {
		resp.Diagnostics.AddError("Option "+name+" isn't managed on "+install,
			"It isn't on the install's whitelist, or it's never managed (siteurl, home and the like). Add it to the whitelist in the project's bootstrap/ and apply that first.")
		return
	}
	if !option.Exists {
		resp.State.RemoveResource(ctx)
		return
	}
	state.ID = types.StringValue(install + "/" + name)

	// A sensitive value is compared through its hash; the plugin returns only
	// the hash for an option whitelisted as sensitive.
	if !state.Value.IsNull() || (state.SensitiveValue.IsNull() && !option.Sensitive) {
		if option.Sensitive {
			resp.Diagnostics.AddError("Option "+name+" is sensitive on "+install, "It's whitelisted as sensitive, so declare it with sensitive_value.")
			return
		}
		state.Value = optionJSONValue(string(option.Value))
		state.SensitiveHash = types.StringNull()
	} else {
		if !option.Sensitive {
			resp.Diagnostics.Append(notSensitive(install, name))
			return
		}
		state.SensitiveHash = types.StringValue(option.SHA256)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

// notSensitive refuses a sensitive_value for an option the whitelist
// doesn't mark as sensitive.
func notSensitive(install, name string) diag.Diagnostic {
	return diag.NewErrorDiagnostic("Option "+name+" isn't sensitive on "+install,
		"The plugin returns its value to every read token, so it can't hold a secret. Whitelist it with sensitive = true in the project's bootstrap/ and apply that first, or declare it with value.")
}

// Delete leaves the value in WordPress: removing a plugin's settings can
// break the plugin (provider D8).
func (r *wordpressOptionResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state wordpressOptionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.AddWarning("Option "+state.Name.ValueString()+" is left in WordPress",
		"It's no longer managed, but its value stays on "+state.Install.ValueString()+". Remove it from the install's whitelist in bootstrap/ if nothing should change it.")
}

// ImportState takes "<install>/<name>".
func (r *wordpressOptionResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	install, name, ok := splitImportID(req.ID, "<install>/<name>", &resp.Diagnostics)
	if !ok {
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("install"), install)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), name)...)
}
