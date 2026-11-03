package provider

import (
	"context"
	"strings"

	"github.com/Metatavu/metatavu-wpengine-opentofu/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                   = &accountUserResource{}
	_ resource.ResourceWithConfigure      = &accountUserResource{}
	_ resource.ResourceWithImportState    = &accountUserResource{}
	_ resource.ResourceWithValidateConfig = &accountUserResource{}
)

// NewAccountUserResource is wpengine_account_user.
func NewAccountUserResource() resource.Resource {
	return &accountUserResource{}
}

type accountUserResource struct {
	client *client.Client
}

type accountUserModel struct {
	ID             types.String `tfsdk:"id"`
	AccountID      types.String `tfsdk:"account_id"`
	Email          types.String `tfsdk:"email"`
	FirstName      types.String `tfsdk:"first_name"`
	LastName       types.String `tfsdk:"last_name"`
	Roles          types.String `tfsdk:"roles"`
	InstallIDs     types.Set    `tfsdk:"install_ids"`
	InviteAccepted types.Bool   `tfsdk:"invite_accepted"`
	MFAEnabled     types.Bool   `tfsdk:"mfa_enabled"`
}

func (r *accountUserResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_account_user"
}

func (r *accountUserResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A person's access to a WP Engine account: its portal, SFTP and SSH. Creating one emails them an invitation. " +
			"Destroying it removes them from the account. Import as `<account_id>/<email>` or `<account_id>/<user_id>`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The user's ID.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"account_id": schema.StringAttribute{Required: true, PlanModifiers: replace},
			"email":      schema.StringAttribute{Required: true, PlanModifiers: replace},
			"first_name": schema.StringAttribute{Required: true, PlanModifiers: replace},
			"last_name":  schema.StringAttribute{Required: true, PlanModifiers: replace},
			"roles": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "`owner`, `full,billing`, `full`, `partial,billing` or `partial`. A `partial` user reaches only its `install_ids`.",
				Validators:          []validator.String{stringvalidator.OneOf("owner", "full,billing", "full", "partial,billing", "partial")},
			},
			"install_ids": schema.SetAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "The installs a `partial` user can reach.",
			},
			"invite_accepted": schema.BoolAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"mfa_enabled": schema.BoolAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *accountUserResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config accountUserModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() || config.Roles.IsUnknown() || config.InstallIDs.IsUnknown() {
		return
	}
	partial := strings.HasPrefix(config.Roles.ValueString(), "partial")
	if partial && config.InstallIDs.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("install_ids"), "A partial user needs installs", "Give install_ids, the installs the user can reach.")
	}
	if !partial && !config.InstallIDs.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("install_ids"), "Only a partial user has installs", "A user with roles "+config.Roles.ValueString()+" reaches every install of the account.")
	}
}

func (r *accountUserResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = configuredPlatform(req.ProviderData, &resp.Diagnostics)
}

func (r *accountUserResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if !requirePlatform(r.client, &resp.Diagnostics) {
		return
	}
	var plan accountUserModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	installIDs := setStrings(ctx, plan.InstallIDs, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	var request client.AccountUserCreate
	request.User.AccountID = plan.AccountID.ValueString()
	request.User.FirstName = plan.FirstName.ValueString()
	request.User.LastName = plan.LastName.ValueString()
	request.User.Email = plan.Email.ValueString()
	request.User.Roles = plan.Roles.ValueString()
	request.User.InstallIDs = installIDs
	user, err := r.client.CreateAccountUser(ctx, plan.AccountID.ValueString(), request)
	if err != nil {
		resp.Diagnostics.AddError("Adding user "+plan.Email.ValueString()+" failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, accountUserState(user, plan))...)
}

func (r *accountUserResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if !requirePlatform(r.client, &resp.Diagnostics) {
		return
	}
	var state accountUserModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	user, err := r.client.GetAccountUser(ctx, state.AccountID.ValueString(), state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading user "+state.Email.ValueString()+" failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, accountUserState(user, state))...)
}

func (r *accountUserResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if !requirePlatform(r.client, &resp.Diagnostics) {
		return
	}
	var plan, state accountUserModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	installIDs := setStrings(ctx, plan.InstallIDs, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	user, err := r.client.UpdateAccountUser(ctx, state.AccountID.ValueString(), state.ID.ValueString(), client.AccountUserUpdate{Roles: plan.Roles.ValueString(), InstallIDs: installIDs})
	if err != nil {
		resp.Diagnostics.AddError("Updating user "+state.Email.ValueString()+" failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, accountUserState(user, plan))...)
}

func (r *accountUserResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if !requirePlatform(r.client, &resp.Diagnostics) {
		return
	}
	var state accountUserModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteAccountUser(ctx, state.AccountID.ValueString(), state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Removing user "+state.Email.ValueString()+" failed", err.Error())
	}
}

// ImportState takes "<account_id>/<email>" or "<account_id>/<user_id>".
func (r *accountUserResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	accountID, ref, ok := splitImportID(req.ID, "<account_id>/<email> or <account_id>/<user_id>", &resp.Diagnostics)
	if !ok || !requirePlatform(r.client, &resp.Diagnostics) {
		return
	}
	userID := ref
	if !uuidPattern.MatchString(ref) {
		users, err := r.client.ListAccountUsers(ctx, accountID)
		if err != nil {
			resp.Diagnostics.AddError("Listing the users of "+accountID+" failed", err.Error())
			return
		}
		userID = ""
		for _, user := range users {
			if strings.EqualFold(user.Email, ref) {
				userID = user.UserID
			}
		}
		if userID == "" {
			resp.Diagnostics.AddError("No such user", "Account "+accountID+" has no user "+ref+".")
			return
		}
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), userID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("account_id"), accountID)...)
}

// accountUserState is the user as WP Engine has it. The email keeps its
// declared case, since WP Engine may store it in another.
func accountUserState(user *client.AccountUser, base accountUserModel) accountUserModel {
	state := accountUserModel{
		ID:             types.StringValue(user.UserID),
		AccountID:      types.StringValue(user.AccountID),
		Email:          types.StringValue(user.Email),
		FirstName:      types.StringValue(user.FirstName),
		LastName:       types.StringValue(user.LastName),
		Roles:          types.StringValue(user.Roles),
		InstallIDs:     types.SetNull(types.StringType),
		InviteAccepted: types.BoolValue(user.InviteAccepted),
		MFAEnabled:     types.BoolValue(user.MFAEnabled),
	}
	if strings.EqualFold(base.Email.ValueString(), user.Email) {
		state.Email = base.Email
	}
	if user.Installs != nil && strings.HasPrefix(user.Roles, "partial") {
		ids := make([]string, len(user.Installs))
		for i, install := range user.Installs {
			ids[i] = install.ID
		}
		state.InstallIDs = stringSet(ids)
	}
	return state
}
