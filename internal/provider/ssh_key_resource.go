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
	"golang.org/x/crypto/ssh"
)

var (
	_ resource.Resource                = &sshKeyResource{}
	_ resource.ResourceWithConfigure   = &sshKeyResource{}
	_ resource.ResourceWithImportState = &sshKeyResource{}
)

// NewSSHKeyResource is wpengine_ssh_key.
func NewSSHKeyResource() resource.Resource {
	return &sshKeyResource{}
}

type sshKeyResource struct {
	client *client.Client
}

type sshKeyModel struct {
	ID          types.String `tfsdk:"id"`
	PublicKey   types.String `tfsdk:"public_key"`
	Fingerprint types.String `tfsdk:"fingerprint"`
	Comment     types.String `tfsdk:"comment"`
	CreatedAt   types.String `tfsdk:"created_at"`
}

func (r *sshKeyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ssh_key"
}

func (r *sshKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	computed := func(description string) schema.StringAttribute {
		return schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: description,
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "An SSH public key of the API user: whoever holds the private key reaches the user's installs through WP Engine's SSH gateway. " +
			"Import by the key's ID or fingerprint.",
		Attributes: map[string]schema.Attribute{
			"id": computed("The key's ID."),
			"public_key": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The public key, as in `authorized_keys`.",
				PlanModifiers:       []planmodifier.String{importedKeyMatches{}},
			},
			"fingerprint": computed("The key's fingerprint, as WP Engine shows it."),
			"comment":     computed(""),
			"created_at":  computed(""),
		},
	}
}

// importedKeyMatches replaces a key when its public key changes. An imported
// key has no public key in state (the API doesn't return it), so its
// fingerprint is compared instead.
type importedKeyMatches struct{}

func (importedKeyMatches) Description(context.Context) string {
	return "Changing the public key replaces the key."
}

func (m importedKeyMatches) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (importedKeyMatches) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() || req.PlanValue.Equal(req.StateValue) {
		return
	}
	if req.StateValue.IsNull() && !req.PlanValue.IsUnknown() {
		var fingerprint types.String
		resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("fingerprint"), &fingerprint)...)
		if fingerprintMatches(req.PlanValue.ValueString(), fingerprint.ValueString()) {
			return
		}
	}
	resp.RequiresReplace = true
}

// fingerprintMatches tells whether a public key has the fingerprint, in
// either of the forms OpenSSH prints.
func fingerprintMatches(publicKey, fingerprint string) bool {
	parsed, _, _, _, err := ssh.ParseAuthorizedKey([]byte(publicKey))
	if err != nil {
		return false
	}
	fingerprint = strings.TrimPrefix(fingerprint, "MD5:")
	return fingerprint == ssh.FingerprintSHA256(parsed) || strings.EqualFold(fingerprint, ssh.FingerprintLegacyMD5(parsed))
}

func (r *sshKeyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = configuredPlatform(req.ProviderData, &resp.Diagnostics)
}

func (r *sshKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if !requirePlatform(r.client, &resp.Diagnostics) {
		return
	}
	var plan sshKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	key, err := r.client.CreateSSHKey(ctx, plan.PublicKey.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Adding the SSH key failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, sshKeyState(key, plan.PublicKey))...)
}

func (r *sshKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if !requirePlatform(r.client, &resp.Diagnostics) {
		return
	}
	var state sshKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	key, err := r.find(ctx, state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Listing SSH keys failed", err.Error())
		return
	}
	if key == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, sshKeyState(key, state.PublicKey))...)
}

// Update is reached only when an imported key gets its public key, which
// already matches: there is nothing to send.
func (r *sshKeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan sshKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *sshKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if !requirePlatform(r.client, &resp.Diagnostics) {
		return
	}
	var state sshKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteSSHKey(ctx, state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Removing SSH key "+state.Fingerprint.ValueString()+" failed", err.Error())
	}
}

// ImportState takes the key's ID or fingerprint.
func (r *sshKeyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !requirePlatform(r.client, &resp.Diagnostics) {
		return
	}
	key, err := r.find(ctx, req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Listing SSH keys failed", err.Error())
		return
	}
	if key == nil {
		resp.Diagnostics.AddError("No such SSH key", "The API user has no SSH key "+req.ID+".")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), key.UUID)...)
}

func (r *sshKeyResource) find(ctx context.Context, ref string) (*client.SSHKey, error) {
	keys, err := r.client.ListSSHKeys(ctx)
	if err != nil {
		return nil, err
	}
	for i := range keys {
		if keys[i].UUID == ref || keys[i].Fingerprint == ref {
			return &keys[i], nil
		}
	}
	return nil, nil
}

func sshKeyState(key *client.SSHKey, publicKey types.String) sshKeyModel {
	return sshKeyModel{
		ID:          types.StringValue(key.UUID),
		PublicKey:   publicKey,
		Fingerprint: types.StringValue(key.Fingerprint),
		Comment:     types.StringValue(key.Comment),
		CreatedAt:   types.StringValue(key.CreatedAt),
	}
}
