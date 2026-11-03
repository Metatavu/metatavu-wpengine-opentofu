package provider

import (
	"context"

	"github.com/Metatavu/metatavu-wpengine-opentofu/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &domainResource{}
	_ resource.ResourceWithConfigure   = &domainResource{}
	_ resource.ResourceWithImportState = &domainResource{}
)

// NewDomainResource is wpengine_domain.
func NewDomainResource() resource.Resource {
	return &domainResource{}
}

type domainResource struct {
	client *client.Client
}

type domainModel struct {
	ID                   types.String `tfsdk:"id"`
	InstallID            types.String `tfsdk:"install_id"`
	Name                 types.String `tfsdk:"name"`
	Primary              types.Bool   `tfsdk:"primary"`
	RedirectTo           types.String `tfsdk:"redirect_to"`
	SecureAllURLs        types.Bool   `tfsdk:"secure_all_urls"`
	Duplicate            types.Bool   `tfsdk:"duplicate"`
	NetworkType          types.String `tfsdk:"network_type"`
	DNSCNAME             types.String `tfsdk:"dns_cname"`
	DNSARecords          types.List   `tfsdk:"dns_a_records"`
	SSLStatus            types.String `tfsdk:"ssl_status"`
	OwnershipStatus      types.String `tfsdk:"ownership_status"`
	TXTVerificationKey   types.String `tfsdk:"txt_verification_key"`
	TXTVerificationValue types.String `tfsdk:"txt_verification_value"`
}

func (r *domainResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_domain"
}

func (r *domainResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	computedString := func(description string) schema.StringAttribute {
		return schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: description,
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A domain or a redirect on an install. A new domain serves traffic only once its ownership is verified: " +
			"add the TXT record `txt_verification_key` = `txt_verification_value` to its DNS. " +
			"WP Engine issues and renews its certificate itself (`ssl_status`). Import as `<install>/<domain>`.",
		Attributes: map[string]schema.Attribute{
			"id": computedString(""),
			"install_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The install the domain points to.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The domain, for example `www.example.com`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"primary": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Whether this is the install's primary domain. A redirect can't be the primary.",
			},
			"redirect_to": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Makes the domain a redirect to another domain of the install, by that domain's `id`. Removing it replaces the domain.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplaceIf(
					func(_ context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
						resp.RequiresReplace = !req.StateValue.IsNull() && req.PlanValue.IsNull()
					},
					"Removing a redirect replaces the domain.", "Removing a redirect replaces the domain.",
				)},
			},
			"secure_all_urls": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Redirect every HTTP request for the domain to HTTPS.",
			},
			"duplicate": schema.BoolAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"network_type":           computedString("WP Engine's network for the domain: `AN` (Advanced Network), `GES` (Global Edge Security) or `LEGACY`."),
			"dns_cname":              computedString("The value for the domain's DNS CNAME record."),
			"ssl_status":             computedString("The state of the certificate WP Engine manages for the domain."),
			"ownership_status":       computedString("`TXT_VERIFIED`, or `TXT_VERIFICATION_PENDING` until the TXT record is in place."),
			"txt_verification_key":   computedString("The TXT record's name, until ownership is verified."),
			"txt_verification_value": computedString("The TXT record's value, until ownership is verified."),
			"dns_a_records": schema.ListAttribute{
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Addresses for the domain's DNS A records, where a CNAME can't be used.",
				PlanModifiers:       []planmodifier.List{listplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *domainResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = configuredPlatform(req.ProviderData, &resp.Diagnostics)
}

func (r *domainResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if !requirePlatform(r.client, &resp.Diagnostics) {
		return
	}
	var plan domainModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	installID, name := plan.InstallID.ValueString(), plan.Name.ValueString()
	domain, err := r.client.CreateDomain(ctx, installID, client.DomainCreate{
		Name:       name,
		Primary:    plan.Primary.ValueBool(),
		RedirectTo: stringPointer(plan.RedirectTo),
	})
	if err != nil {
		resp.Diagnostics.AddError("Adding domain "+name+" failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, domainState(domain, installID))...)
	// Creating a domain can't set secure_all_urls.
	if plan.SecureAllURLs.ValueBool() {
		secure := true
		if domain, err = r.client.UpdateDomain(ctx, installID, domain.ID, client.DomainUpdate{SecureAllURLs: &secure}); err != nil {
			resp.Diagnostics.AddError("Securing domain "+name+" failed", err.Error())
			return
		}
		resp.Diagnostics.Append(resp.State.Set(ctx, domainState(domain, installID))...)
	}
}

func (r *domainResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if !requirePlatform(r.client, &resp.Diagnostics) {
		return
	}
	var state domainModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	domain, err := r.client.GetDomain(ctx, state.InstallID.ValueString(), state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading domain "+state.Name.ValueString()+" failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, domainState(domain, state.InstallID.ValueString()))...)
}

func (r *domainResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if !requirePlatform(r.client, &resp.Diagnostics) {
		return
	}
	var plan, state domainModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	update := client.DomainUpdate{}
	if !plan.Primary.Equal(state.Primary) {
		primary := plan.Primary.ValueBool()
		update.Primary = &primary
	}
	if !plan.RedirectTo.Equal(state.RedirectTo) {
		update.RedirectTo = stringPointer(plan.RedirectTo)
	}
	if !plan.SecureAllURLs.Equal(state.SecureAllURLs) {
		secure := plan.SecureAllURLs.ValueBool()
		update.SecureAllURLs = &secure
	}
	installID := state.InstallID.ValueString()
	domain, err := r.client.UpdateDomain(ctx, installID, state.ID.ValueString(), update)
	if err != nil {
		resp.Diagnostics.AddError("Updating domain "+state.Name.ValueString()+" failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, domainState(domain, installID))...)
}

func (r *domainResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if !requirePlatform(r.client, &resp.Diagnostics) {
		return
	}
	var state domainModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteDomain(ctx, state.InstallID.ValueString(), state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Removing domain "+state.Name.ValueString()+" failed", err.Error())
	}
}

// ImportState takes "<install>/<domain>": the install's ID or name, and the
// domain's ID or name.
func (r *domainResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	installRef, domainRef, ok := splitImportID(req.ID, "<install>/<domain>", &resp.Diagnostics)
	if !ok || !requirePlatform(r.client, &resp.Diagnostics) {
		return
	}
	installID, err := resolveInstall(ctx, r.client, installRef)
	if err != nil {
		resp.Diagnostics.AddError("Finding install "+installRef+" failed", err.Error())
		return
	}
	domains, err := r.client.ListDomains(ctx, installID)
	if err != nil {
		resp.Diagnostics.AddError("Listing the domains of "+installRef+" failed", err.Error())
		return
	}
	for _, domain := range domains {
		if domain.ID == domainRef || domain.Name == domainRef {
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), domain.ID)...)
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("install_id"), installID)...)
			return
		}
	}
	resp.Diagnostics.AddError("No such domain", "Install "+installRef+" has no domain "+domainRef+".")
}

func domainState(domain *client.Domain, installID string) domainModel {
	state := domainModel{
		ID:                   types.StringValue(domain.ID),
		InstallID:            types.StringValue(installID),
		Name:                 types.StringValue(domain.Name),
		Primary:              types.BoolValue(domain.Primary),
		RedirectTo:           types.StringNull(),
		SecureAllURLs:        types.BoolValue(domain.SecureAllURLs),
		Duplicate:            types.BoolValue(domain.Duplicate),
		NetworkType:          types.StringValue(domain.NetworkType),
		DNSCNAME:             types.StringNull(),
		DNSARecords:          stringList(nil),
		SSLStatus:            types.StringNull(),
		OwnershipStatus:      types.StringValue(domain.OwnershipStatus),
		TXTVerificationKey:   stringValue(domain.TXTVerificationKey),
		TXTVerificationValue: stringValue(domain.TXTVerificationValue),
	}
	if len(domain.RedirectsTo) > 0 {
		state.RedirectTo = types.StringValue(domain.RedirectsTo[0].ID)
	}
	if details := domain.NetworkDetails; details != nil {
		if dns := details.DNSConfigInfo; dns != nil {
			state.DNSCNAME = types.StringValue(dns.CNAME)
			state.DNSARecords = stringList(dns.ARecords)
		}
		if network := details.NetworkInfo; network != nil && network.SSL != nil {
			state.SSLStatus = types.StringValue(network.SSL.Status)
		}
	}
	return state
}
