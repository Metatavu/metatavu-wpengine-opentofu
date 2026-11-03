package provider

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/Metatavu/metatavu-wpengine-opentofu/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &installResource{}
	_ resource.ResourceWithConfigure   = &installResource{}
	_ resource.ResourceWithImportState = &installResource{}
)

// A new install is pending until WP Engine has set it up. Create waits for it,
// checking every installPollInterval for at most installTimeout; tests shorten
// the interval.
var (
	installPollInterval = 15 * time.Second
	installTimeout      = 30 * time.Minute
)

// NewInstallResource is wpengine_install.
func NewInstallResource() resource.Resource {
	return &installResource{}
}

type installResource struct {
	client *client.Client
}

type installModel struct {
	ID            types.String `tfsdk:"id"`
	AccountID     types.String `tfsdk:"account_id"`
	Name          types.String `tfsdk:"name"`
	SiteID        types.String `tfsdk:"site_id"`
	Environment   types.String `tfsdk:"environment"`
	PHPVersion    types.String `tfsdk:"php_version"`
	CNAME         types.String `tfsdk:"cname"`
	PrimaryDomain types.String `tfsdk:"primary_domain"`
	WPVersion     types.String `tfsdk:"wp_version"`
	Status        types.String `tfsdk:"status"`
	StableIPs     types.List   `tfsdk:"stable_ips"`
	IsMultisite   types.Bool   `tfsdk:"is_multisite"`
}

func (r *installResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_install"
}

func (r *installResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A WP Engine install: one WordPress environment of a site. " +
			"Its `php_version` is declared but can't be applied, because the WP Engine API can't change it: " +
			"a difference shows in the plan, and apply refuses until the version is changed in the portal. " +
			"Destroying an install deletes it and everything in it, with no undo. Import by ID or by name.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"account_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The WP Engine account the install belongs to.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The install's name, unique across WP Engine: 3–14 lowercase letters and digits, starting with a letter. It's also the install's `<name>.wpengine.com` address.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators: []validator.String{
					stringvalidator.RegexMatches(regexp.MustCompile(`^[a-z][a-z0-9]{2,13}$`), "must be 3–14 lowercase letters and digits, starting with a letter"),
				},
			},
			"site_id": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "The site the install belongs to. Changing it moves the install.",
			},
			"environment": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "The install's environment type in its site: `production`, `staging` or `development`. Changed in place.",
				Validators:          []validator.String{stringvalidator.OneOf("production", "staging", "development")},
			},
			"php_version": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The PHP version the install runs, for example `8.4`. Declared, read from WP Engine, and never applied: change it in the portal.",
			},
			"cname": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The install's WP Engine address, `<name>.wpengine.com`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"primary_domain": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"wp_version": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The WordPress version, which WP Engine keeps up to date.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"status": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"stable_ips": schema.ListAttribute{
				Computed:      true,
				ElementType:   types.StringType,
				PlanModifiers: []planmodifier.List{listplanmodifier.UseStateForUnknown()},
			},
			"is_multisite": schema.BoolAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *installResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = configuredPlatform(req.ProviderData, &resp.Diagnostics)
}

func (r *installResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if !requirePlatform(r.client, &resp.Diagnostics) {
		return
	}
	var plan installModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	name := plan.Name.ValueString()
	created, err := r.client.CreateInstall(ctx, client.InstallCreate{
		Name:        name,
		AccountID:   plan.AccountID.ValueString(),
		SiteID:      stringPointer(plan.SiteID),
		Environment: stringPointer(plan.Environment),
	})
	if err != nil {
		resp.Diagnostics.AddError("Creating install "+name+" failed", err.Error())
		return
	}
	// Saved first, so a failed wait leaves the install in state, not orphaned.
	resp.Diagnostics.Append(resp.State.Set(ctx, installState(created, plan))...)

	install, err := r.waitActive(ctx, created.ID)
	if err != nil {
		resp.Diagnostics.AddError("Install "+name+" didn't become active", err.Error())
		return
	}
	// The state keeps the declared PHP version, so the next plan shows a
	// difference (D6).
	if live := install.PHPVersion; live != nil && *live != plan.PHPVersion.ValueString() {
		resp.Diagnostics.AddWarning("Install "+name+" runs another PHP version",
			fmt.Sprintf("WP Engine created it with PHP %s, not %s. Change it in the WP Engine portal; the next plan shows the difference until then.", *live, plan.PHPVersion.ValueString()))
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, installState(install, plan))...)
}

func (r *installResource) waitActive(ctx context.Context, id string) (*client.Install, error) {
	deadline := time.Now().Add(installTimeout)
	for {
		install, err := r.client.GetInstall(ctx, id)
		if err != nil {
			return nil, err
		}
		if install.Status != client.InstallPending {
			return install, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("still pending after %s; the next apply reads it again", installTimeout)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(installPollInterval):
		}
	}
}

func (r *installResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if !requirePlatform(r.client, &resp.Diagnostics) {
		return
	}
	var state installModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	install, err := r.client.GetInstall(ctx, state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading install "+state.Name.ValueString()+" failed", err.Error())
		return
	}
	read := installState(install, state)
	if install.PHPVersion != nil {
		read.PHPVersion = types.StringValue(*install.PHPVersion)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, read)...)
}

func (r *installResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if !requirePlatform(r.client, &resp.Diagnostics) {
		return
	}
	var plan, state installModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	name := state.Name.ValueString()
	if !plan.PHPVersion.Equal(state.PHPVersion) {
		resp.Diagnostics.AddAttributeError(path.Root("php_version"), "Change PHP in the WP Engine portal",
			fmt.Sprintf("Install %s runs PHP %s. The WP Engine API can't change it: change it to %s in the WP Engine portal, then plan again.",
				name, state.PHPVersion.ValueString(), plan.PHPVersion.ValueString()))
		return
	}
	if plan.SiteID.IsNull() && !state.SiteID.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("site_id"), "An install can't leave its site", "WP Engine can move install "+name+" to another site, but not out of every site.")
		return
	}
	if plan.Environment.IsNull() && !state.Environment.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("environment"), "An install's environment type can't be removed", "Give install "+name+" another environment type instead.")
		return
	}
	update := client.InstallUpdate{}
	if !plan.SiteID.Equal(state.SiteID) {
		update.SiteID = stringPointer(plan.SiteID)
	}
	if !plan.Environment.Equal(state.Environment) {
		update.Environment = stringPointer(plan.Environment)
	}
	install, err := r.client.UpdateInstall(ctx, state.ID.ValueString(), update)
	if err != nil {
		resp.Diagnostics.AddError("Updating install "+name+" failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, installState(install, plan))...)
}

func (r *installResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if !requirePlatform(r.client, &resp.Diagnostics) {
		return
	}
	var state installModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteInstall(ctx, state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting install "+state.Name.ValueString()+" failed", err.Error())
	}
}

// ImportState takes an install's ID or its name.
func (r *installResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if uuidPattern.MatchString(req.ID) {
		resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
		return
	}
	if !requirePlatform(r.client, &resp.Diagnostics) {
		return
	}
	installs, err := r.client.ListInstalls(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Listing installs failed", err.Error())
		return
	}
	for _, install := range installs {
		if install.Name == req.ID {
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), install.ID)...)
			return
		}
	}
	resp.Diagnostics.AddError("No such install", "The API user sees no install named "+req.ID+".")
}

// installState is the install as WP Engine has it, with the PHP version from
// base: Read replaces it with the live one.
func installState(install *client.Install, base installModel) installModel {
	state := installModel{
		ID:            types.StringValue(install.ID),
		AccountID:     types.StringValue(install.Account.ID),
		Name:          types.StringValue(install.Name),
		SiteID:        types.StringNull(),
		Environment:   stringValue(install.Environment),
		PHPVersion:    base.PHPVersion,
		CNAME:         types.StringValue(install.CNAME),
		PrimaryDomain: stringValue(install.PrimaryDomain),
		WPVersion:     stringValue(install.WPVersion),
		Status:        types.StringValue(install.Status),
		StableIPs:     stringList(install.StableIPs),
		IsMultisite:   types.BoolNull(),
	}
	if install.Site != nil {
		state.SiteID = types.StringValue(install.Site.ID)
	}
	if install.IsMultisite != nil {
		state.IsMultisite = types.BoolValue(*install.IsMultisite)
	}
	return state
}
